package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"topup-backend/config"
	"topup-backend/internal/domain"
	appcrypto "topup-backend/internal/pkg/crypto"
	"topup-backend/internal/repository"
)

var (
	ErrKiosgamerSessionExpired    = errors.New("kiosgamer session expired")
	ErrKiosgamerReauthRequired    = errors.New("kiosgamer re-authentication required")
	ErrKiosgamerChallengeRequired = errors.New("kiosgamer anti-bot challenge required")
	ErrKiosgamerNotConfigured     = errors.New("kiosgamer session is not configured")
)

type KiosgamerUserInfo struct {
	PlayerID interface{} `json:"player_id"`
	OAuth    *struct {
		Platform         int     `json:"platform"`
		Username         string  `json:"username"`
		ExpiryTime       int64   `json:"expiry_time"`
		UUID             string  `json:"uuid"`
		UID              int64   `json:"uid"`
		ShellBalance     float64 `json:"shell_balance"`
		UAC              string  `json:"uac"`
		IsGarenaVerified bool    `json:"is_garena_verified"`
	} `json:"oauth"`
}

type KiosgamerSSOSession struct {
	Login                bool   `json:"login"`
	Platform             int    `json:"platform"`
	Username             string `json:"username"`
	UID                  int64  `json:"uid"`
	SessionKey           string `json:"session_key"`
	ExpiryTime           int64  `json:"expiry_time"`
	Timestamp            int64  `json:"timestamp"`
	NotifyPasswordUpdate bool   `json:"notify_password_update"`
}

type KiosgamerStatus struct {
	Configured      bool       `json:"configured"`
	Status          string     `json:"status"`
	Username        string     `json:"username,omitempty"`
	UID             string     `json:"uid,omitempty"`
	OAuthExpiryTime int64      `json:"oauth_expiry_time,omitempty"`
	HasTOTPSecret   bool       `json:"has_totp_secret"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	LastRecoveredAt *time.Time `json:"last_recovered_at,omitempty"`
}

// KiosgamerOrderResult is the result returned by PlaceOrder
type KiosgamerOrderResult struct {
	OrderID      string `json:"order_id"`
	Status       string `json:"status"` // "success", "pending", "failed"
	Message      string `json:"message"`
	SerialNumber string `json:"serial_number,omitempty"`
}

// KiosgamerCatalogItem represents a product in Kiosgamer's shop catalog
type KiosgamerCatalogItem struct {
	ItemID       int     `json:"item_id"`
	ProductCode  string  `json:"product_code"`
	Name         string  `json:"name"`
	Amount       int     `json:"amount"`
	PointName    string  `json:"point_name"`
	PriceIDR     float64 `json:"price_idr"`
	GarenaShell  int     `json:"garena_shell"`
	ItemType     string  `json:"item_type"` // "points", "membership", "bundle"
	AppID        int     `json:"app_id"`
	Description  string  `json:"description,omitempty"`
}

// KiosgamerSyncResult holds the summary of an auto-sync mapping operation
type KiosgamerSyncResult struct {
	GameID         uint     `json:"game_id"`
	GameName       string   `json:"game_name"`
	GameSlug       string   `json:"game_slug"`
	TotalNominals  int      `json:"total_nominals"`
	MatchedCount   int      `json:"matched_count"`
	UnmatchedCount int      `json:"unmatched_count"`
	MatchedItems   []string `json:"matched_items"`
	UnmatchedItems []string `json:"unmatched_items"`
	Message        string   `json:"message"`
}

type KiosgamerService interface {
	SaveCredentials(sessionKey, totpSecret string) error
	Status(ctx context.Context) (*KiosgamerStatus, error)
	HealthCheck(ctx context.Context) (*KiosgamerUserInfo, error)
	EnsureSession(ctx context.Context) (*KiosgamerUserInfo, error)
	RecoverSession(ctx context.Context) error
	GenerateTOTP(at time.Time) (string, error)
	PlaceOrder(ctx context.Context, refID, productCode, customerID, serverID, gameSlug string) (*KiosgamerOrderResult, error)

	// Catalog & Mapping
	FetchCatalog(ctx context.Context, gameSlug string) ([]KiosgamerCatalogItem, error)
	AutoSyncMapping(ctx context.Context, gameID uint, gameSlug string) (*KiosgamerSyncResult, error)
	UpdateNominalKiosgamerCode(nominalID uint, kiosgamerCode string) error
}

type kiosgamerService struct {
	repo         repository.KiosgamerRepository
	providerRepo repository.ProviderRepository
	nominalRepo  repository.NominalRepository
	gameRepo     repository.GameRepository
	cfg          *config.Config
	httpClient   *http.Client
	baseURL      *url.URL
	mu           sync.Mutex
}

func NewKiosgamerService(
	repo repository.KiosgamerRepository,
	providerRepo repository.ProviderRepository,
	nominalRepo repository.NominalRepository,
	gameRepo repository.GameRepository,
	cfg *config.Config,
) KiosgamerService {
	jar, _ := cookiejar.New(nil)
	baseURL, _ := url.Parse(strings.TrimRight(cfg.KiosgamerBaseURL, "/"))
	return &kiosgamerService{
		repo:         repo,
		providerRepo: providerRepo,
		nominalRepo:  nominalRepo,
		gameRepo:     gameRepo,
		cfg:          cfg,
		httpClient:   &http.Client{Timeout: 35 * time.Second, Jar: jar},
		baseURL:      baseURL,
	}
}

func (s *kiosgamerService) provider() (*domain.Provider, error) {
	p, err := s.providerRepo.GetByCode("KIOSGAMER")
	if err != nil {
		return nil, fmt.Errorf("KIOSGAMER provider is not configured: %w", err)
	}
	return p, nil
}

func (s *kiosgamerService) SaveCredentials(sessionKey, totpSecret string) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	current, err := s.repo.GetByProviderID(p.ID)
	if err != nil {
		return err
	}
	if current == nil {
		current = &domain.KiosgamerCredential{ProviderID: p.ID, SessionStatus: domain.KiosgamerStatusUnknown}
	}

	if sessionKey != "" {
		enc, err := appcrypto.EncryptString(sessionKey, s.cfg.AppSecret)
		if err != nil {
			return err
		}
		current.SessionKeyEncrypted = enc
		s.setSessionCookie(sessionKey)
	}
	if totpSecret != "" {
		normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(totpSecret), " ", ""))
		if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized); err != nil {
			return fmt.Errorf("invalid TOTP secret")
		}
		enc, err := appcrypto.EncryptString(normalized, s.cfg.AppSecret)
		if err != nil {
			return err
		}
		current.TOTPSecretEncrypted = enc
	}
	return s.repo.Upsert(current)
}

func (s *kiosgamerService) loadCredential() (*domain.KiosgamerCredential, string, error) {
	p, err := s.provider()
	if err != nil {
		return nil, "", err
	}
	cred, err := s.repo.GetByProviderID(p.ID)
	if err != nil {
		return nil, "", err
	}
	if cred == nil || cred.SessionKeyEncrypted == "" {
		return cred, "", ErrKiosgamerNotConfigured
	}
	sessionKey, err := appcrypto.DecryptString(cred.SessionKeyEncrypted, s.cfg.AppSecret)
	if err != nil {
		return nil, "", err
	}
	s.setSessionCookie(sessionKey)
	return cred, sessionKey, nil
}

func (s *kiosgamerService) setSessionCookie(sessionKey string) {
	if sessionKey == "" || s.baseURL == nil || s.httpClient.Jar == nil {
		return
	}
	s.httpClient.Jar.SetCookies(s.baseURL, []*http.Cookie{{
		Name: "session_key", Value: sessionKey, Path: "/", Secure: true, HttpOnly: true,
	}})
}

func (s *kiosgamerService) doJSON(ctx context.Context, method, path string, payload interface{}, out interface{}) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	endpoint := strings.TrimRight(s.cfg.KiosgamerBaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Referer", "https://kiosgamer.co.id/")
	req.Header.Set("User-Agent", "IRXPlay-Kiosgamer-Provider/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && resp.Header.Get("X-DD-B") != "" {
		resp.Body.Close()
		return nil, ErrKiosgamerChallengeRequired
	}

	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return resp, ErrKiosgamerSessionExpired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, fmt.Errorf("kiosgamer HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp, fmt.Errorf("decode Kiosgamer response: %w", err)
		}
	}
	return resp, nil
}

func (s *kiosgamerService) HealthCheck(ctx context.Context) (*KiosgamerUserInfo, error) {
	cred, _, err := s.loadCredential()
	if err != nil {
		return nil, err
	}

	var info KiosgamerUserInfo
	_, err = s.doJSON(ctx, http.MethodGet, "/auth/get_user_info/multi", nil, &info)
	now := time.Now()
	cred.LastCheckedAt = &now
	if err != nil || info.OAuth == nil {
		cred.SessionStatus = domain.KiosgamerStatusExpired
		_ = s.repo.Upsert(cred)
		if err != nil {
			return nil, err
		}
		return nil, ErrKiosgamerSessionExpired
	}

	cred.SessionStatus = domain.KiosgamerStatusActive
	cred.AccountUsername = info.OAuth.Username
	cred.AccountUID = strconv.FormatInt(info.OAuth.UID, 10)
	cred.OAuthExpiryTime = info.OAuth.ExpiryTime
	_ = s.repo.Upsert(cred)
	return &info, nil
}

func (s *kiosgamerService) EnsureSession(ctx context.Context) (*KiosgamerUserInfo, error) {
	info, err := s.HealthCheck(ctx)
	if err == nil {
		return info, nil
	}
	if !errors.Is(err, ErrKiosgamerSessionExpired) {
		return nil, err
	}

	if err := s.RecoverSession(ctx); err != nil {
		return nil, err
	}
	return s.HealthCheck(ctx)
}

func (s *kiosgamerService) RecoverSession(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, _, err := s.loadCredential()
	if err != nil {
		return err
	}

	var sso KiosgamerSSOSession
	_, err = s.doJSON(ctx, http.MethodGet, "/auth/check_session", nil, &sso)
	if err != nil {
		if errors.Is(err, ErrKiosgamerChallengeRequired) {
			cred.SessionStatus = domain.KiosgamerStatusChallengeRequired
		} else {
			cred.SessionStatus = domain.KiosgamerStatusReauthRequired
		}
		_ = s.repo.Upsert(cred)
		return err
	}
	if !sso.Login || sso.SessionKey == "" {
		cred.SessionStatus = domain.KiosgamerStatusReauthRequired
		_ = s.repo.Upsert(cred)
		return ErrKiosgamerReauthRequired
	}

	var result map[string]interface{}
	resp, err := s.doJSON(ctx, http.MethodPost, "/auth/sso", map[string]string{"session_key": sso.SessionKey}, &result)
	if err != nil {
		return err
	}

	var newSession string
	if s.httpClient.Jar != nil && s.baseURL != nil {
		for _, cookie := range s.httpClient.Jar.Cookies(s.baseURL) {
			if cookie.Name == "session_key" {
				newSession = cookie.Value
				break
			}
		}
	}
	if newSession == "" && resp != nil {
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "session_key" {
				newSession = cookie.Value
				break
			}
		}
	}
	if newSession == "" {
		return errors.New("Kiosgamer SSO recovery succeeded but no new session_key was returned")
	}

	enc, err := appcrypto.EncryptString(newSession, s.cfg.AppSecret)
	if err != nil {
		return err
	}
	now := time.Now()
	cred.SessionKeyEncrypted = enc
	cred.SessionStatus = domain.KiosgamerStatusActive
	cred.LastRecoveredAt = &now
	if sso.Username != "" {
		cred.AccountUsername = sso.Username
	}
	if sso.UID != 0 {
		cred.AccountUID = strconv.FormatInt(sso.UID, 10)
	}
	if sso.ExpiryTime != 0 {
		cred.OAuthExpiryTime = sso.ExpiryTime
	}
	return s.repo.Upsert(cred)
}

func (s *kiosgamerService) Status(ctx context.Context) (*KiosgamerStatus, error) {
	p, err := s.provider()
	if err != nil {
		return nil, err
	}
	cred, err := s.repo.GetByProviderID(p.ID)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return &KiosgamerStatus{Configured: false, Status: domain.KiosgamerStatusUnknown}, nil
	}
	return &KiosgamerStatus{
		Configured:      cred.SessionKeyEncrypted != "",
		Status:          cred.SessionStatus,
		Username:        cred.AccountUsername,
		UID:             cred.AccountUID,
		OAuthExpiryTime: cred.OAuthExpiryTime,
		HasTOTPSecret:   cred.TOTPSecretEncrypted != "",
		LastCheckedAt:   cred.LastCheckedAt,
		LastRecoveredAt: cred.LastRecoveredAt,
	}, nil
}

func (s *kiosgamerService) GenerateTOTP(at time.Time) (string, error) {
	p, err := s.provider()
	if err != nil {
		return "", err
	}
	cred, err := s.repo.GetByProviderID(p.ID)
	if err != nil {
		return "", err
	}
	if cred == nil || cred.TOTPSecretEncrypted == "" {
		return "", errors.New("Kiosgamer TOTP secret is not configured")
	}
	secret, err := appcrypto.DecryptString(cred.TOTPSecretEncrypted, s.cfg.AppSecret)
	if err != nil {
		return "", err
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", errors.New("invalid stored TOTP secret")
	}

	counter := uint64(at.Unix() / 30)
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	binaryCode := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", binaryCode%1000000), nil
}

// ── PlaceOrder ────────────────────────────────────────────────────────────────
// Sends a top-up order to Kiosgamer for Free Fire and CODM products.
// The flow:
//  1. Validate player ID via Kiosgamer's player-lookup endpoint (game-specific).
//  2. Submit the purchase using Garena Shell balance.
//  3. If a TOTP is needed (OTP step), generate and submit it automatically.
//
// productCode: item_id Kiosgamer (angka, contoh: "14", "30", "103")
// customerID: player UID (FF: playerID, CODM: playerID)
// serverID:   zone/server ID (FF: zone ID, CODM: kosong untuk global)
// gameSlug:   slug game ("free-fire" atau "call-of-duty-mobile") untuk menentukan app_id yang benar
func (s *kiosgamerService) PlaceOrder(ctx context.Context, refID, productCode, customerID, serverID, gameSlug string) (*KiosgamerOrderResult, error) {
	if _, err := s.EnsureSession(ctx); err != nil {
		return nil, fmt.Errorf("kiosgamer: session error before order: %w", err)
	}

	// ── Step 1: Player validation ──────────────────────────────────────────
	// Resolve app_id dari gameSlug (lebih akurat daripada dari productCode angka)
	// Jika gameSlug kosong, fallback ke deteksi dari productCode
	appIDSource := gameSlug
	if appIDSource == "" {
		appIDSource = productCode
	}
	appID := s.resolveAppID(appIDSource)

	playerPayload := map[string]interface{}{
		"player_id": customerID,
		"app_id":    appID,
	}
	if serverID != "" {
		playerPayload["server_id"] = serverID
	}

	var playerResp struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Nickname string `json:"username"`
			Valid    bool   `json:"valid"`
		} `json:"data"`
	}
	_, err := s.doJSON(ctx, http.MethodPost, "/topup/validate_player", playerPayload, &playerResp)
	if err != nil {
		// HTTP 404 dari validate_player = produk/player tidak ditemukan di Kiosgamer (bukan session error)
		// Kembalikan sebagai "failed" bukan error agar tidak mislabeled sebagai "Session Error"
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "error_not_found") || strings.Contains(err.Error(), "1005") {
			return &KiosgamerOrderResult{
				Status:  "failed",
				Message: fmt.Sprintf("Player ID %s atau produk tidak ditemukan di server Kiosgamer (app_id: %d). Pastikan Player ID benar dan produk tersedia.", customerID, appID),
			}, nil
		}
		return nil, fmt.Errorf("kiosgamer: player validation failed: %w", err)
	}
	if !playerResp.Data.Valid {
		return &KiosgamerOrderResult{
			Status:  "failed",
			Message: fmt.Sprintf("Player ID %s tidak ditemukan atau tidak valid di server Kiosgamer. Periksa kembali ID dan Server/Zone.", customerID),
		}, nil
	}

	// ── Step 2: Submit purchase order ──────────────────────────────────────
	orderPayload := map[string]interface{}{
		"product_code": productCode,
		"player_id":    customerID,
		"app_id":       appID,
		"ref_id":       refID,
	}
	if serverID != "" {
		orderPayload["server_id"] = serverID
	}

	var orderResp struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
		Data    *struct {
			OrderID      string `json:"order_id"`
			Status       string `json:"status"`
			SerialNumber string `json:"sn"`
			Message      string `json:"message"`
			// OTP challenge fields
			NeedOTP bool   `json:"need_otp"`
			OTPKey  string `json:"otp_key"`
		} `json:"data"`
	}
	_, err = s.doJSON(ctx, http.MethodPost, "/topup/order", orderPayload, &orderResp)
	if err != nil {
		// Session may have expired mid-order; return specific error
		if errors.Is(err, ErrKiosgamerSessionExpired) {
			return nil, err
		}
		return &KiosgamerOrderResult{
			Status:  "failed",
			Message: fmt.Sprintf("Kiosgamer order failed: %v", err),
		}, nil
	}

	if orderResp.Data == nil {
		return &KiosgamerOrderResult{
			Status:  "failed",
			Message: fmt.Sprintf("Kiosgamer order: respons kosong (status %d): %s", orderResp.Status, orderResp.Message),
		}, nil
	}

	// ── Step 3: Handle OTP/TOTP challenge if required ──────────────────────
	if orderResp.Data.NeedOTP {
		totp, err := s.GenerateTOTP(time.Now())
		if err != nil {
			return &KiosgamerOrderResult{
				OrderID: orderResp.Data.OrderID,
				Status:  "failed",
				Message: "TOTP secret tidak terkonfigurasi; tidak dapat menyelesaikan OTP challenge Kiosgamer",
			}, fmt.Errorf("kiosgamer OTP required but TOTP not configured: %w", err)
		}

		otpPayload := map[string]interface{}{
			"order_id": orderResp.Data.OrderID,
			"otp_key":  orderResp.Data.OTPKey,
			"otp":      totp,
		}
		var otpResp struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
			Data    *struct {
				OrderID      string `json:"order_id"`
				Status       string `json:"status"`
				SerialNumber string `json:"sn"`
				Message      string `json:"message"`
			} `json:"data"`
		}
		_, err = s.doJSON(ctx, http.MethodPost, "/topup/verify_otp", otpPayload, &otpResp)
		if err != nil {
			return &KiosgamerOrderResult{
				OrderID: orderResp.Data.OrderID,
				Status:  "failed",
				Message: fmt.Sprintf("OTP verification failed: %v", err),
			}, nil
		}
		if otpResp.Data != nil {
			return s.buildOrderResult(otpResp.Data.OrderID, otpResp.Data.Status, otpResp.Data.Message, otpResp.Data.SerialNumber), nil
		}
	}

	return s.buildOrderResult(orderResp.Data.OrderID, orderResp.Data.Status, orderResp.Data.Message, orderResp.Data.SerialNumber), nil
}

// resolveAppID maps a product code or game slug to the Garena game app_id.
// Free Fire (FF):  app_id = 100067
// Call of Duty Mobile (CODM): app_id = 100082
func (s *kiosgamerService) resolveAppID(identifier string) int {
	code := strings.ToLower(strings.TrimSpace(identifier))
	switch {
	case strings.Contains(code, "codm"), strings.Contains(code, "call-of-duty"), strings.Contains(code, "duty"):
		return 100082
	default:
		// Default to Free Fire
		return 100067
	}
}

func (s *kiosgamerService) buildOrderResult(orderID, status, message, sn string) *KiosgamerOrderResult {
	normalized := strings.ToLower(strings.TrimSpace(status))
	resultStatus := "pending"
	switch normalized {
	case "success", "sukses", "completed", "done":
		resultStatus = "success"
	case "failed", "gagal", "error", "cancel":
		resultStatus = "failed"
	}
	return &KiosgamerOrderResult{
		OrderID:      orderID,
		Status:       resultStatus,
		Message:      message,
		SerialNumber: sn,
	}
}

// ── FetchCatalog ─────────────────────────────────────────────────────────────
// Retrieves the real-time product list from Kiosgamer's public channels endpoint.
// Prioritizes the official "Garena Shells" payment channel (Channel 208070 / Currency GS)
// to get the exact Garena Shell cost and accurate item_id.
func (s *kiosgamerService) FetchCatalog(ctx context.Context, gameSlug string) ([]KiosgamerCatalogItem, error) {
	appID := s.resolveAppID(gameSlug)
	endpoint := fmt.Sprintf("https://kiosgamer.co.id/api/shop/apps/channels?app_id=%d&region=CO.ID&language=id", appID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create catalog request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://kiosgamer.co.id/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Kiosgamer catalog: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiosgamer catalog returned HTTP status %d", resp.StatusCode)
	}

	var parsed struct {
		AppInfo struct {
			AppID     int    `json:"app_id"`
			AppName   string `json:"app_name"`
			PointName string `json:"point_name"`
		} `json:"app_info"`
		Channels []struct {
			Channel  int    `json:"channel"`
			Name     string `json:"name"`
			Currency string `json:"currency"`
			Items    []struct {
				ItemID            int     `json:"item_id"`
				AppPointAmount    int     `json:"app_point_amount"`
				GarenaPointAmount int     `json:"garena_point_amount"`
				CurrencyAmount    float64 `json:"currency_amount"`
				RebateCardID      int     `json:"rebate_card_id"`
				AppItemID         int64   `json:"app_item_id"`
				RebateCard        *struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"rebate_card"`
				AppItem *struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"app_item"`
			} `json:"items"`
		} `json:"channels"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("failed to decode Kiosgamer catalog: %w", err)
	}

	pointName := parsed.AppInfo.PointName
	if pointName == "" {
		if appID == 100082 {
			pointName = "CP"
		} else {
			pointName = "Diamond"
		}
	}

	// 1. Build a price lookup from IDR channels (like QRIS or ShopeePay) for reference
	idrPriceByAmount := make(map[int]float64)
	idrPriceByName := make(map[string]float64)
	for _, ch := range parsed.Channels {
		if ch.Currency == "IDR" {
			for _, it := range ch.Items {
				if it.AppPointAmount > 0 && idrPriceByAmount[it.AppPointAmount] == 0 {
					idrPriceByAmount[it.AppPointAmount] = it.CurrencyAmount
				}
				if it.RebateCard != nil && it.RebateCard.Name != "" {
					idrPriceByName[strings.ToLower(it.RebateCard.Name)] = it.CurrencyAmount
				}
				if it.AppItem != nil && it.AppItem.Name != "" {
					idrPriceByName[strings.ToLower(it.AppItem.Name)] = it.CurrencyAmount
				}
			}
		}
	}

	var items []KiosgamerCatalogItem
	seenItemIDs := make(map[int]bool)

	// 2. Primary: Extract items from "Garena Shells" channel (Channel 208070 or currency == "GS")
	var shellChannelIndex = -1
	for i, ch := range parsed.Channels {
		if ch.Channel == 208070 || strings.EqualFold(ch.Name, "Garena Shells") {
			shellChannelIndex = i
			break
		}
	}

	if shellChannelIndex >= 0 {
		shellCh := parsed.Channels[shellChannelIndex]
		for _, it := range shellCh.Items {
			seenItemIDs[it.ItemID] = true

			itemType := "points"
			itemName := ""
			itemDesc := ""
			shellCount := int(it.CurrencyAmount) // exact Garena Shell count, e.g. 3, 24, 33, 66, 100, 300, 150

			if it.RebateCard != nil && it.RebateCard.Name != "" {
				itemType = "membership"
				itemName = it.RebateCard.Name
				itemDesc = it.RebateCard.Description
			} else if it.AppItem != nil && it.AppItem.Name != "" {
				itemType = "bundle"
				itemName = it.AppItem.Name
				itemDesc = it.AppItem.Description
			} else if it.AppPointAmount > 0 {
				itemName = fmt.Sprintf("%d %s", it.AppPointAmount, pointName)
			} else {
				itemName = fmt.Sprintf("Item #%d", it.ItemID)
			}

			// Estimate or lookup IDR price (1 Shell ≈ Rp 330 or exact IDR channel price)
			priceIDR := float64(shellCount * 330)
			if it.AppPointAmount > 0 && idrPriceByAmount[it.AppPointAmount] > 0 {
				priceIDR = idrPriceByAmount[it.AppPointAmount]
			} else if idrPriceByName[strings.ToLower(itemName)] > 0 {
				priceIDR = idrPriceByName[strings.ToLower(itemName)]
			}

			items = append(items, KiosgamerCatalogItem{
				ItemID:      it.ItemID,
				ProductCode: strconv.Itoa(it.ItemID),
				Name:        itemName,
				Amount:      it.AppPointAmount,
				PointName:   pointName,
				PriceIDR:    priceIDR,
				GarenaShell: shellCount,
				ItemType:    itemType,
				AppID:       parsed.AppInfo.AppID,
				Description: itemDesc,
			})
		}
	}

	// 3. Fallback: If any other items exist in IDR channels not in shell channel, include them
	for _, ch := range parsed.Channels {
		for _, it := range ch.Items {
			if seenItemIDs[it.ItemID] {
				continue
			}
			seenItemIDs[it.ItemID] = true

			itemType := "points"
			itemName := ""
			itemDesc := ""
			shellCount := int(it.GarenaPointAmount / 100)
			if shellCount == 0 && it.CurrencyAmount > 0 {
				shellCount = int(it.CurrencyAmount / 330)
			}

			if it.RebateCard != nil && it.RebateCard.Name != "" {
				itemType = "membership"
				itemName = it.RebateCard.Name
				itemDesc = it.RebateCard.Description
			} else if it.AppItem != nil && it.AppItem.Name != "" {
				itemType = "bundle"
				itemName = it.AppItem.Name
				itemDesc = it.AppItem.Description
			} else if it.AppPointAmount > 0 {
				itemName = fmt.Sprintf("%d %s", it.AppPointAmount, pointName)
			} else {
				itemName = fmt.Sprintf("Item #%d", it.ItemID)
			}

			items = append(items, KiosgamerCatalogItem{
				ItemID:      it.ItemID,
				ProductCode: strconv.Itoa(it.ItemID),
				Name:        itemName,
				Amount:      it.AppPointAmount,
				PointName:   pointName,
				PriceIDR:    it.CurrencyAmount,
				GarenaShell: shellCount,
				ItemType:    itemType,
				AppID:       parsed.AppInfo.AppID,
				Description: itemDesc,
			})
		}
	}

	return items, nil
}

// ── AutoSyncMapping ──────────────────────────────────────────────────────────
// Automatically maps database Nominals with Kiosgamer Catalog Items based on amount / name.
func (s *kiosgamerService) AutoSyncMapping(ctx context.Context, gameID uint, gameSlug string) (*KiosgamerSyncResult, error) {
	catalog, err := s.FetchCatalog(ctx, gameSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Kiosgamer catalog: %w", err)
	}

	nominals, err := s.nominalRepo.ListByGameID(gameID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch nominals for game %d: %w", gameID, err)
	}

	game, _ := s.gameRepo.FindByID(gameID)
	gameName := "Game"
	if game != nil {
		gameName = game.Name
	}

	result := &KiosgamerSyncResult{
		GameID:         gameID,
		GameName:       gameName,
		GameSlug:       gameSlug,
		TotalNominals:  len(nominals),
		MatchedItems:   make([]string, 0),
		UnmatchedItems: make([]string, 0),
	}

	for i := range nominals {
		nom := &nominals[i]
		matched := false
		nomNameLower := strings.ToLower(nom.Name)

		// 1. Try matching by membership/special name
		for _, cat := range catalog {
			catNameLower := strings.ToLower(cat.Name)
			if cat.ItemType == "membership" || cat.ItemType == "bundle" {
				// E.g. "Member Mingguan", "Mingguan Lite", "Member Bulanan", "BP Card"
				if strings.Contains(nomNameLower, "lite") && strings.Contains(catNameLower, "lite") {
					nom.KiosgamerProductCode = cat.ProductCode
					_ = s.nominalRepo.Update(nom)
					result.MatchedCount++
					result.MatchedItems = append(result.MatchedItems, fmt.Sprintf("%s ➔ Kiosgamer ID %s (%s - %d Shell)", nom.Name, cat.ProductCode, cat.Name, cat.GarenaShell))
					matched = true
					break
				}
				if strings.Contains(nomNameLower, "mingguan") && !strings.Contains(nomNameLower, "lite") && strings.Contains(catNameLower, "mingguan") && !strings.Contains(catNameLower, "lite") {
					nom.KiosgamerProductCode = cat.ProductCode
					_ = s.nominalRepo.Update(nom)
					result.MatchedCount++
					result.MatchedItems = append(result.MatchedItems, fmt.Sprintf("%s ➔ Kiosgamer ID %s (%s - %d Shell)", nom.Name, cat.ProductCode, cat.Name, cat.GarenaShell))
					matched = true
					break
				}
				if strings.Contains(nomNameLower, "bulanan") && strings.Contains(catNameLower, "bulanan") {
					nom.KiosgamerProductCode = cat.ProductCode
					_ = s.nominalRepo.Update(nom)
					result.MatchedCount++
					result.MatchedItems = append(result.MatchedItems, fmt.Sprintf("%s ➔ Kiosgamer ID %s (%s - %d Shell)", nom.Name, cat.ProductCode, cat.Name, cat.GarenaShell))
					matched = true
					break
				}
				if (strings.Contains(nomNameLower, "bp card") || strings.Contains(nomNameLower, "battle pass")) && (strings.Contains(catNameLower, "bp") || strings.Contains(catNameLower, "pass")) {
					nom.KiosgamerProductCode = cat.ProductCode
					_ = s.nominalRepo.Update(nom)
					result.MatchedCount++
					result.MatchedItems = append(result.MatchedItems, fmt.Sprintf("%s ➔ Kiosgamer ID %s (%s - %d Shell)", nom.Name, cat.ProductCode, cat.Name, cat.GarenaShell))
					matched = true
					break
				}
			}
		}

		if matched {
			continue
		}

		// 2. Try matching by point amount (e.g. 5, 50, 70, 140, 355, 720, 7290, 63 CP, 128 CP, etc)
		for _, cat := range catalog {
			if cat.ItemType == "points" && cat.Amount > 0 {
				amountStr := strconv.Itoa(cat.Amount)
				// Check if the nominal name contains the exact amount with word boundaries
				words := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(nomNameLower, "dm", " dm"), "cp", " cp"))
				isAmountMatch := false
				for _, w := range words {
					if w == amountStr || w == amountStr+"dm" || w == amountStr+"cp" || w == amountStr+"diamond" {
						isAmountMatch = true
						break
					}
				}

				if isAmountMatch {
					nom.KiosgamerProductCode = cat.ProductCode
					_ = s.nominalRepo.Update(nom)
					result.MatchedCount++
					result.MatchedItems = append(result.MatchedItems, fmt.Sprintf("%s ➔ Kiosgamer ID %s (%d %s - %d Shell)", nom.Name, cat.ProductCode, cat.Amount, cat.PointName, cat.GarenaShell))
					matched = true
					break
				}
			}
		}

		if !matched {
			result.UnmatchedCount++
			result.UnmatchedItems = append(result.UnmatchedItems, fmt.Sprintf("%s (Tidak ditemukan produk setara di Kiosgamer)", nom.Name))
		}
	}

	result.Message = fmt.Sprintf("Auto-Sync SKU selesai: %d dari %d produk berhasil dimapping otomatis ke Kiosgamer.", result.MatchedCount, result.TotalNominals)
	return result, nil
}

// ── UpdateNominalKiosgamerCode ──────────────────────────────────────────────
// Manually update or override the Kiosgamer Product Code for a nominal.
func (s *kiosgamerService) UpdateNominalKiosgamerCode(nominalID uint, kiosgamerCode string) error {
	nom, err := s.nominalRepo.FindByID(nominalID)
	if err != nil {
		return err
	}
	if nom == nil {
		return errors.New("nominal not found")
	}
	nom.KiosgamerProductCode = strings.TrimSpace(kiosgamerCode)
	return s.nominalRepo.Update(nom)
}
