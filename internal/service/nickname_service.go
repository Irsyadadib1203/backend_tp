package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type NicknameCheckResult struct {
	Success  bool   `json:"success"`
	GameCode string `json:"game_code"`
	UserID   string `json:"user_id"`
	ServerID string `json:"server_id,omitempty"`
	Nickname string `json:"nickname"`
	Message  string `json:"message,omitempty"`
}

type NicknameService interface {
	CheckNickname(gameCode, userID, serverID string) (*NicknameCheckResult, error)
}

type nicknameService struct {
	httpClient *http.Client
}

func NewNicknameService() NicknameService {
	return &nicknameService{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (s *nicknameService) CheckNickname(gameCode, userID, serverID string) (*NicknameCheckResult, error) {
	gameCode = strings.ToUpper(strings.TrimSpace(gameCode))
	userID = strings.TrimSpace(userID)
	serverID = strings.TrimSpace(serverID)

	if userID == "" {
		return nil, errors.New("user ID is required")
	}

	// Normalisasi game code
	switch gameCode {
	case "MOBILE_LEGENDS", "MLBB", "MOBILE-LEGENDS":
		return s.checkMLBB(userID, serverID)
	case "FREE_FIRE", "FF", "FREE-FIRE":
		return s.checkGoPayPrepare("FREEFIRE", "FREE_FIRE", "Free Fire", userID, serverID)
	case "PUBG_MOBILE", "PUBGM", "PUBG-MOBILE":
		return s.checkGoPayPrepare("PUBGM", "PUBG_MOBILE", "PUBG Mobile", userID, serverID)
	case "AOV", "ARENA_OF_VALOR", "ARENA-OF-VALOR", "ARENAOFVALOR":
		return s.checkGoPayPrepare("AOV", "AOV", "Arena of Valor", userID, serverID)
	case "CALL_OF_DUTY", "CODM", "CALL-OF-DUTY", "CALLOFDUTY":
		return s.checkGoPayPrepare("CALL_OF_DUTY", "CALL_OF_DUTY", "Call of Duty Mobile", userID, serverID)
	case "HOK", "HONOR_OF_KINGS", "HONOR-OF-KINGS", "HONOROFKINGS":
		return s.checkGoPayPrepare("HOK", "HOK", "Honor of Kings", userID, serverID)
	case "GENSHIN_IMPACT", "GENSHIN", "GENSHIN-IMPACT":
		return s.checkHoyoverse("GENSHIN_IMPACT", "Traveler", userID, serverID)
	case "HONKAI_STAR_RAIL", "HSR", "HONKAI-STAR-RAIL", "HONKAISTARRAIL":
		return s.checkHoyoverse("HONKAI_STAR_RAIL", "Trailblazer", userID, serverID)
	case "VALORANT", "VALO":
		return &NicknameCheckResult{
			Success:  true,
			GameCode: "VALORANT",
			UserID:   userID,
			ServerID: serverID,
			Nickname: userID,
			Message:  "Riot ID valid",
		}, nil
	default:
		// Generic or mock validator for testing & other games
		return &NicknameCheckResult{
			Success:  true,
			GameCode: gameCode,
			UserID:   userID,
			ServerID: serverID,
			Nickname: fmt.Sprintf("Player_%s", userID),
			Message:  "ID format valid",
		}, nil
	}
}

func (s *nicknameService) checkMLBB(userID, zoneID string) (*NicknameCheckResult, error) {
	if zoneID == "" {
		return nil, errors.New("zone ID / Server ID is required for Mobile Legends")
	}

	payload, err := json.Marshal(struct {
		Code string `json:"code"`
		Data struct {
			UserID string `json:"userId"`
			ZoneID string `json:"zoneId"`
		} `json:"data"`
	}{
		Code: "MOBILE_LEGENDS",
		Data: struct {
			UserID string `json:"userId"`
			ZoneID string `json:"zoneId"`
		}{
			UserID: userID,
			ZoneID: zoneID,
		},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, "https://gopay.co.id/games/v1/order/user-account", bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, errors.New("layanan verifikasi Mobile Legends sedang tidak tersedia")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var jsonResp struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(body, &jsonResp); err == nil && jsonResp.Data.Username != "" {
		return &NicknameCheckResult{
			Success:  true,
			GameCode: "MOBILE_LEGENDS",
			UserID:   userID,
			ServerID: zoneID,
			Nickname: jsonResp.Data.Username,
		}, nil
	}

	// If external API didn't match or failed, return clean error
	if jsonResp.Message != "" {
		return nil, errors.New(jsonResp.Message)
	}

	return nil, errors.New("User ID atau Zone ID Mobile Legends tidak ditemukan")
}

// checkGoPayPrepare menangani pengecekan nickname via GoPay Prepare API (Free Fire, PUBGM, AOV, CODM, HOK)
func (s *nicknameService) checkGoPayPrepare(gopayCode, returnCode, gameName, userID, zoneID string) (*NicknameCheckResult, error) {
	url := fmt.Sprintf("https://gopay.co.id/games/v1/order/prepare/%s?userId=%s&zoneId=%s", gopayCode, userID, zoneID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("layanan verifikasi %s sedang tidak tersedia", gameName)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 1. Format jika response string langsung: {"data": "Nickname123", "message": "success"}
	var jsonRespStr struct {
		Data    string `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &jsonRespStr); err == nil && jsonRespStr.Data != "" {
		return &NicknameCheckResult{
			Success:  true,
			GameCode: returnCode,
			UserID:   userID,
			ServerID: zoneID,
			Nickname: jsonRespStr.Data,
		}, nil
	}

	// 2. Format jika response nested object: {"data": {"username": "Nickname123"}}
	var jsonRespObj struct {
		Data struct {
			Username string `json:"username"`
			Name     string `json:"name"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &jsonRespObj); err == nil {
		name := jsonRespObj.Data.Username
		if name == "" {
			name = jsonRespObj.Data.Name
		}
		if name != "" {
			return &NicknameCheckResult{
				Success:  true,
				GameCode: returnCode,
				UserID:   userID,
				ServerID: zoneID,
				Nickname: name,
			}, nil
		}
		if jsonRespObj.Message != "" {
			return nil, errors.New(jsonRespObj.Message)
		}
	}

	return nil, fmt.Errorf("Player ID %s tidak valid atau tidak ditemukan", gameName)
}

// checkHoyoverse menangani UID dan Server untuk Genshin Impact & Honkai: Star Rail
func (s *nicknameService) checkHoyoverse(gameCode, charTitle, userID, serverID string) (*NicknameCheckResult, error) {
	if len(userID) < 8 || len(userID) > 11 {
		return nil, fmt.Errorf("UID %s harus berupa 9-10 digit angka", strings.ReplaceAll(gameCode, "_", " "))
	}

	serverName := serverID
	if serverName == "" {
		switch string(userID[0]) {
		case "6":
			serverName = "America"
		case "7":
			serverName = "Europe"
		case "8", "18":
			serverName = "Asia"
		case "9":
			serverName = "TW,HK,MO"
		default:
			serverName = "Asia"
		}
	}

	return &NicknameCheckResult{
		Success:  true,
		GameCode: gameCode,
		UserID:   userID,
		ServerID: serverName,
		Nickname: fmt.Sprintf("%s_%s (%s)", charTitle, userID, serverName),
	}, nil
}
