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

const kiosgamerGarenaShellChannelID = 208070

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
	ItemID      int     `json:"item_id"`
	ProductCode string  `json:"product_code"`
	Name        string  `json:"name"`
	Amount      int     `json:"amount"`
	PointName   string  `json:"point_name"`
	PriceIDR    float64 `json:"price_idr"`
	GarenaShell int     `json:"garena_shell"`
	ItemType    string  `json:"item_type"` // "points", "membership", "bundle"
	AppID       int     `json:"app_id"`
	Description string  `json:"description,omitempty"`
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
	orderMu      sync.Mutex
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

func (s *kiosgamerService) cookieValue(name string) string {
	if s.httpClient == nil || s.httpClient.Jar == nil || s.baseURL == nil {
		return ""
	}
	for _, cookie := range s.httpClient.Jar.Cookies(s.baseURL) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func (s *kiosgamerService) doJSON(ctx context.Context, method, path string, payload interface{}, out interface{}) (*http.Response, error) {
	return s.doJSONWithHeaders(ctx, method, path, payload, out, nil)
}

func (s *kiosgamerService) doJSONWithHeaders(ctx context.Context, method, path string, payload interface{}, out interface{}, headers map[string]string) (*http.Response, error) {
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
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && resp.Header.Get("X-DD-B") != "" {
		resp.Body.Close()
		return nil, fmt.Errorf("%w at %s %s", ErrKiosgamerChallengeRequired, method, path)
	}

	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return resp, fmt.Errorf("%w at %s %s", ErrKiosgamerSessionExpired, method, path)
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
		switch {
		case errors.Is(err, ErrKiosgamerChallengeRequired):
			cred.SessionStatus = domain.KiosgamerStatusChallengeRequired
		case errors.Is(err, ErrKiosgamerReauthRequired):
			cred.SessionStatus = domain.KiosgamerStatusReauthRequired
		default:
			cred.SessionStatus = domain.KiosgamerStatusExpired
		}
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
// Executes the flow observed from Kiosgamer's web client:
//  1. Ensure/repair Kiosgamer + Garena SSO session.
//  2. POST /auth/player_id_login to bind the target player to this session.
//  3. GET /shop/apps/roles and select packed_role_id (Free Fire commonly returns 0).
//  4. POST /preflight, then read the __csrf__ cookie.
//  5. Generate the current TOTP and POST /shop/pay/init using the Garena Shell channel.
//  6. Poll /shop/pay/poll using the returned display_id.
//
// Kiosgamer keeps player/login state in the session cookie. Therefore the stateful
// player-login -> pay-init section is serialized with orderMu so two orders cannot
// overwrite each other's selected player inside the same Kiosgamer session.
func (s *kiosgamerService) PlaceOrder(ctx context.Context, refID, productCode, customerID, serverID, gameSlug string) (*KiosgamerOrderResult, error) {
	_ = refID // Kiosgamer uses display_id as its provider transaction identifier.

	itemID, err := strconv.Atoi(strings.TrimSpace(productCode))
	if err != nil || itemID <= 0 {
		return &KiosgamerOrderResult{Status: "failed", Message: "item_id Kiosgamer tidak valid: " + productCode}, nil
	}
	if strings.TrimSpace(customerID) == "" {
		return &KiosgamerOrderResult{Status: "failed", Message: "Player ID Kiosgamer kosong"}, nil
	}

	appID := s.resolveAppID(gameSlug)

	// The session stores player context. Keep this section single-flight per account.
	s.orderMu.Lock()

	info, err := s.EnsureSession(ctx)
	if err != nil {
		s.orderMu.Unlock()
		return nil, fmt.Errorf("kiosgamer: session error before order: %w", err)
	}

	// Best-effort refresh of the Garena SSO session. A healthy Kiosgamer session
	// is already enough to continue the order flow; check_session can legitimately
	// report no separate Garena web session even while get_user_info/multi is valid.
	// Do not turn that condition into a hard Reauth Required error.
	if err := s.ensureGarenaTransactionSession(ctx); err != nil {
		if errors.Is(err, ErrKiosgamerChallengeRequired) || errors.Is(err, ErrKiosgamerSessionExpired) {
			s.orderMu.Unlock()
			return nil, err
		}
		// Keep using the healthy Kiosgamer session returned by EnsureSession.
	}

	// Step 1: bind/validate the target player.
	playerPayload := map[string]interface{}{
		"app_id":   appID,
		"login_id": customerID,
	}
	if v := strings.TrimSpace(serverID); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil {
			playerPayload["app_server_id"] = n
		} else {
			playerPayload["app_server_id"] = v
		}
	}

	var playerResp struct {
		OpenID   string `json:"open_id"`
		Region   string `json:"region"`
		Nickname string `json:"nickname"`
		ImgURL   string `json:"img_url"`
	}
	if _, err := s.doJSON(ctx, http.MethodPost, "/auth/player_id_login", playerPayload, &playerResp); err != nil {
		s.orderMu.Unlock()
		if errors.Is(err, ErrKiosgamerSessionExpired) || errors.Is(err, ErrKiosgamerChallengeRequired) {
			return nil, err
		}
		return &KiosgamerOrderResult{Status: "failed", Message: fmt.Sprintf("Player ID %s gagal divalidasi Kiosgamer: %v", customerID, err)}, nil
	}
	if playerResp.OpenID == "" {
		s.orderMu.Unlock()
		return &KiosgamerOrderResult{Status: "failed", Message: fmt.Sprintf("Player ID %s tidak ditemukan di Kiosgamer", customerID)}, nil
	}

	// Step 2: obtain the packed role selected by Kiosgamer for the player.
	packedRoleID, err := s.fetchPackedRoleID(ctx, appID, serverID)
	if err != nil {
		s.orderMu.Unlock()
		return &KiosgamerOrderResult{Status: "failed", Message: fmt.Sprintf("Gagal mengambil role/player Kiosgamer: %v", err)}, nil
	}

	// Step 3: preflight sets/refreshes CSRF state used by pay/init.
	if _, err := s.doJSON(ctx, http.MethodPost, "/preflight", nil, nil); err != nil {
		s.orderMu.Unlock()
		return nil, fmt.Errorf("kiosgamer preflight failed: %w", err)
	}
	csrfToken := s.cookieValue("__csrf__")
	if csrfToken == "" {
		s.orderMu.Unlock()
		return &KiosgamerOrderResult{Status: "failed", Message: "Kiosgamer tidak mengembalikan CSRF token setelah preflight"}, nil
	}

	// Generate the OTP as late as possible. Avoid using a token in its final seconds.
	if sec := time.Now().Unix() % 30; sec >= 27 {
		wait := time.Duration(30-sec+1) * time.Second
		select {
		case <-ctx.Done():
			s.orderMu.Unlock()
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	otpCode, err := s.GenerateTOTP(time.Now())
	if err != nil {
		s.orderMu.Unlock()
		return nil, fmt.Errorf("kiosgamer TOTP generation failed: %w", err)
	}

	garenaUID := int64(0)
	if info != nil && info.OAuth != nil {
		garenaUID = info.OAuth.UID
	}
	if garenaUID == 0 {
		if status, statusErr := s.Status(ctx); statusErr == nil {
			garenaUID, _ = strconv.ParseInt(status.UID, 10, 64)
		}
	}
	if garenaUID == 0 {
		s.orderMu.Unlock()
		return &KiosgamerOrderResult{Status: "failed", Message: "Garena UID akun Kiosgamer tidak tersedia"}, nil
	}

	payPayload := map[string]interface{}{
		"app_id":         appID,
		"packed_role_id": packedRoleID,
		"channel_id":     kiosgamerGarenaShellChannelID,
		"service":        "pc",
		"item_id":        itemID,
		"channel_data": map[string]interface{}{
			"otp_code":   otpCode,
			"garena_uid": garenaUID,
		},
	}

	var initResp struct {
		DisplayID string      `json:"display_id"`
		Result    string      `json:"result"`
		ErrorData interface{} `json:"error_data"`
		Exec      struct {
			DisplayID string `json:"display_id"`
		} `json:"exec"`
	}
	_, err = s.doJSONWithHeaders(ctx, http.MethodPost, "/shop/pay/init?region=CO.ID&language=id", payPayload, &initResp, map[string]string{
		"x-csrf-token": csrfToken,
	})

	// Player context is no longer needed after pay/init, so allow the next order to start.
	s.orderMu.Unlock()

	if err != nil {
		if errors.Is(err, ErrKiosgamerSessionExpired) || errors.Is(err, ErrKiosgamerChallengeRequired) {
			return nil, err
		}
		return &KiosgamerOrderResult{Status: "failed", Message: fmt.Sprintf("Kiosgamer pay/init gagal: %v", err)}, nil
	}
	if initResp.DisplayID == "" {
		initResp.DisplayID = initResp.Exec.DisplayID
	}
	if !strings.EqualFold(initResp.Result, "success") || initResp.DisplayID == "" {
		errMsg := "Kiosgamer menolak transaksi"
		if initResp.ErrorData != nil {
			if b, marshalErr := json.Marshal(initResp.ErrorData); marshalErr == nil {
				errMsg += ": " + string(b)
			}
		}
		return &KiosgamerOrderResult{OrderID: initResp.DisplayID, Status: "failed", Message: errMsg}, nil
	}

	// Step 5: poll using display_id. The browser does the same on /result.
	return s.pollKiosgamerOrder(ctx, initResp.DisplayID)
}

func (s *kiosgamerService) ensureGarenaTransactionSession(ctx context.Context) error {
	var session KiosgamerSSOSession
	_, err := s.doJSON(ctx, http.MethodGet, "/auth/check_session", nil, &session)
	if err != nil {
		return fmt.Errorf("kiosgamer Garena SSO check failed: %w", err)
	}
	if !session.Login || session.SessionKey == "" {
		// No separate Garena browser session is available for silent SSO refresh.
		// This is not the same as an expired Kiosgamer session: PlaceOrder has
		// already verified the current Kiosgamer session with EnsureSession.
		return nil
	}

	var ignored map[string]interface{}
	resp, err := s.doJSON(ctx, http.MethodPost, "/auth/sso", map[string]string{"session_key": session.SessionKey}, &ignored)
	if err != nil {
		return fmt.Errorf("kiosgamer Garena SSO exchange failed: %w", err)
	}

	newSession := s.cookieValue("session_key")
	if newSession == "" && resp != nil {
		for _, cookie := range resp.Cookies() {
			if cookie.Name == "session_key" {
				newSession = cookie.Value
				break
			}
		}
	}
	if newSession == "" {
		return errors.New("kiosgamer SSO exchange did not return session_key")
	}

	p, err := s.provider()
	if err != nil {
		return err
	}
	cred, err := s.repo.GetByProviderID(p.ID)
	if err != nil {
		return err
	}
	if cred == nil {
		return ErrKiosgamerNotConfigured
	}
	enc, err := appcrypto.EncryptString(newSession, s.cfg.AppSecret)
	if err != nil {
		return err
	}
	cred.SessionKeyEncrypted = enc
	cred.SessionStatus = domain.KiosgamerStatusActive
	if session.UID != 0 {
		cred.AccountUID = strconv.FormatInt(session.UID, 10)
	}
	now := time.Now()
	cred.LastRecoveredAt = &now
	return s.repo.Upsert(cred)
}

func (s *kiosgamerService) fetchPackedRoleID(ctx context.Context, appID int, serverID string) (int64, error) {
	params := url.Values{}
	params.Set("app_id", strconv.Itoa(appID))
	params.Set("region", "CO.ID")
	params.Set("language", "id")
	params.Set("source", "pc")
	if strings.TrimSpace(serverID) != "" {
		params.Set("app_server_id", strings.TrimSpace(serverID))
	}

	var raw map[string]json.RawMessage
	if _, err := s.doJSON(ctx, http.MethodGet, "/shop/apps/roles?"+params.Encode(), nil, &raw); err != nil {
		return 0, err
	}

	entriesRaw, ok := raw[strconv.Itoa(appID)]
	if !ok {
		// Free Fire commonly uses packed_role_id=0, but only fall back when the
		// endpoint returned a valid JSON object without an explicit error field.
		if errRaw, hasErr := raw["error"]; hasErr && len(errRaw) > 0 {
			return 0, fmt.Errorf("roles endpoint error: %s", strings.TrimSpace(string(errRaw)))
		}
		return 0, nil
	}

	var roles []struct {
		PackedRoleID int64  `json:"packed_role_id"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(entriesRaw, &roles); err != nil {
		return 0, fmt.Errorf("decode roles response: %w", err)
	}
	for _, role := range roles {
		if role.Error == "" {
			return role.PackedRoleID, nil
		}
	}
	if len(roles) > 0 && roles[0].Error != "" {
		return 0, errors.New(roles[0].Error)
	}
	return 0, nil
}

func (s *kiosgamerService) pollKiosgamerOrder(ctx context.Context, displayID string) (*KiosgamerOrderResult, error) {
	delays := []time.Duration{0, 2 * time.Second, 3 * time.Second, 5 * time.Second, 8 * time.Second}
	var last map[string]interface{}

	for _, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return &KiosgamerOrderResult{OrderID: displayID, Status: "pending", Message: "Polling Kiosgamer dihentikan oleh context"}, nil
			case <-time.After(delay):
			}
		}

		var poll map[string]interface{}
		_, err := s.doJSON(ctx, http.MethodPost, "/shop/pay/poll?region=CO.ID&language=id", map[string]string{"display_id": displayID}, &poll)
		if err != nil {
			if errors.Is(err, ErrKiosgamerSessionExpired) || errors.Is(err, ErrKiosgamerChallengeRequired) {
				return nil, err
			}
			// Do not create/retry a new order after init succeeded. Keep it pending.
			last = map[string]interface{}{"message": err.Error()}
			continue
		}
		last = poll

		status, message, serial := parseKiosgamerPoll(poll)
		switch status {
		case "success":
			if message == "" {
				message = "Kiosgamer top up berhasil"
			}
			return &KiosgamerOrderResult{OrderID: displayID, Status: "success", Message: message, SerialNumber: serial}, nil
		case "failed":
			if message == "" {
				message = "Kiosgamer melaporkan transaksi gagal"
			}
			return &KiosgamerOrderResult{OrderID: displayID, Status: "failed", Message: message, SerialNumber: serial}, nil
		}
	}

	msg := "Transaksi Kiosgamer sudah dibuat dan masih diproses"
	if last != nil {
		if b, err := json.Marshal(last); err == nil {
			msg += "; respons terakhir: " + truncateString(string(b), 500)
		}
	}
	return &KiosgamerOrderResult{OrderID: displayID, Status: "pending", Message: msg}, nil
}

func parseKiosgamerPoll(v map[string]interface{}) (status, message, serial string) {
	// Prefer explicit fields from the top-level response.
	status = normalizeKiosgamerStatus(firstString(v, "status", "result", "state", "transaction_status"))
	message = firstString(v, "message", "msg", "error", "error_message")
	serial = firstString(v, "sn", "serial_number", "serial", "voucher_code")

	// Some responses nest the transaction under data/transaction/exec.
	for _, key := range []string{"data", "transaction", "exec"} {
		if nested, ok := v[key].(map[string]interface{}); ok {
			if status == "pending" || status == "" {
				status = normalizeKiosgamerStatus(firstString(nested, "status", "result", "state", "transaction_status"))
			}
			if message == "" {
				message = firstString(nested, "message", "msg", "error", "error_message")
			}
			if serial == "" {
				serial = firstString(nested, "sn", "serial_number", "serial", "voucher_code")
			}
		}
	}
	if status == "" {
		status = "pending"
	}
	return status, message, serial
}

func normalizeKiosgamerStatus(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "success", "sukses", "completed", "complete", "done", "paid":
		return "success"
	case "failed", "fail", "gagal", "error", "cancel", "cancelled", "canceled", "expired":
		return "failed"
	case "pending", "processing", "process", "queued", "created", "waiting", "":
		return "pending"
	default:
		return "pending"
	}
}

func firstString(v map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := v[key]
		if !ok || raw == nil {
			continue
		}
		switch x := raw.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				return x
			}
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(x)
		}
	}
	return ""
}

func truncateString(v string, max int) string {
	if len(v) <= max {
		return v
	}
	return v[:max] + "..."
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
