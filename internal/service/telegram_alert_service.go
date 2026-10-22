package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"topup-backend/internal/pkg/telegram"
	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

// ---------------------------------------------------------------------------
// TelegramSettings — configurable via Admin Panel, stored in system_settings
// ---------------------------------------------------------------------------

// TelegramSettings holds all Telegram alert configuration.
type TelegramSettings struct {
	Enabled      bool               `json:"enabled"`
	BotToken     string             `json:"bot_token"`
	ChatID       string             `json:"chat_id"`
	MinBalance   map[string]float64 `json:"min_balance"`   // provider_code -> threshold
	StuckMinutes int                `json:"stuck_minutes"` // default 5
}

// System setting keys
const (
	keyTelegramEnabled      = "telegram_enabled"
	keyTelegramBotToken     = "telegram_bot_token"
	keyTelegramChatID       = "telegram_chat_id"
	keyTelegramMinBalance   = "telegram_min_balance"
	keyTelegramStuckMinutes = "telegram_stuck_minutes"
)

// ---------------------------------------------------------------------------
// TelegramAlertService interface
// ---------------------------------------------------------------------------

// TelegramAlertService handles periodic Telegram alert checks for
// low provider balances and stuck transactions.
type TelegramAlertService interface {
	// RunAlertCheck is called by the scheduler every N minutes.
	RunAlertCheck(ctx context.Context) error
	// GetSettings returns the current Telegram configuration.
	GetSettings() (*TelegramSettings, error)
	// SaveSettings persists updated Telegram configuration.
	SaveSettings(settings *TelegramSettings) error
	// SendTestMessage sends a test notification to validate the bot setup.
	SendTestMessage() error
}

// ---------------------------------------------------------------------------
// Implementation
// ---------------------------------------------------------------------------

type telegramAlertService struct {
	settingRepo  repository.SystemSettingRepository
	providerRepo repository.ProviderRepository
	txRepo       repository.TransactionRepository
	registry     *provider.Registry

	// Cooldown tracking to avoid spamming the same alert.
	lastAlerts map[string]time.Time
	mu         sync.Mutex
}

// NewTelegramAlertService wires the service with the repositories and
// provider registry already initialised by main.go.
func NewTelegramAlertService(
	settingRepo repository.SystemSettingRepository,
	providerRepo repository.ProviderRepository,
	txRepo repository.TransactionRepository,
	registry *provider.Registry,
) TelegramAlertService {
	return &telegramAlertService{
		settingRepo:  settingRepo,
		providerRepo: providerRepo,
		txRepo:       txRepo,
		registry:     registry,
		lastAlerts:   make(map[string]time.Time),
	}
}

// -----------------------------------------------------------------------
// Settings CRUD
// -----------------------------------------------------------------------

func (s *telegramAlertService) GetSettings() (*TelegramSettings, error) {
	settings := &TelegramSettings{
		Enabled:      false,
		MinBalance:   make(map[string]float64),
		StuckMinutes: 5,
	}

	if v, err := s.settingRepo.Get(keyTelegramEnabled); err == nil {
		settings.Enabled = strings.EqualFold(v.Value, "true")
	}
	if v, err := s.settingRepo.Get(keyTelegramBotToken); err == nil {
		settings.BotToken = v.Value
	}
	if v, err := s.settingRepo.Get(keyTelegramChatID); err == nil {
		settings.ChatID = v.Value
	}
	if v, err := s.settingRepo.Get(keyTelegramMinBalance); err == nil {
		_ = json.Unmarshal([]byte(v.Value), &settings.MinBalance)
	}
	if v, err := s.settingRepo.Get(keyTelegramStuckMinutes); err == nil {
		if mins, err := strconv.Atoi(v.Value); err == nil && mins > 0 {
			settings.StuckMinutes = mins
		}
	}

	return settings, nil
}

func (s *telegramAlertService) SaveSettings(settings *TelegramSettings) error {
	enabled := "false"
	if settings.Enabled {
		enabled = "true"
	}
	if err := s.settingRepo.Set(keyTelegramEnabled, enabled); err != nil {
		return fmt.Errorf("simpan telegram_enabled: %w", err)
	}
	if err := s.settingRepo.Set(keyTelegramBotToken, settings.BotToken); err != nil {
		return fmt.Errorf("simpan telegram_bot_token: %w", err)
	}
	if err := s.settingRepo.Set(keyTelegramChatID, settings.ChatID); err != nil {
		return fmt.Errorf("simpan telegram_chat_id: %w", err)
	}

	minBalanceJSON, _ := json.Marshal(settings.MinBalance)
	if err := s.settingRepo.Set(keyTelegramMinBalance, string(minBalanceJSON)); err != nil {
		return fmt.Errorf("simpan telegram_min_balance: %w", err)
	}

	stuckStr := strconv.Itoa(settings.StuckMinutes)
	if settings.StuckMinutes <= 0 {
		stuckStr = "5"
	}
	if err := s.settingRepo.Set(keyTelegramStuckMinutes, stuckStr); err != nil {
		return fmt.Errorf("simpan telegram_stuck_minutes: %w", err)
	}

	return nil
}

// -----------------------------------------------------------------------
// Test message
// -----------------------------------------------------------------------

func (s *telegramAlertService) SendTestMessage() error {
	settings, err := s.GetSettings()
	if err != nil {
		return fmt.Errorf("gagal memuat settings: %w", err)
	}
	if settings.BotToken == "" || settings.ChatID == "" {
		return fmt.Errorf("bot token dan chat ID harus diisi terlebih dahulu")
	}

	client := telegram.NewClient(settings.BotToken)

	wib := time.FixedZone("WIB", 7*3600)
	now := time.Now().In(wib).Format("02/01/2006 15:04:05 WIB")

	msg := fmt.Sprintf(
		"✅ *Telegram Bot Connected!*\n\n"+
			"Bot Telegram berhasil terhubung dengan sistem TopUp.\n"+
			"Notifikasi akan dikirim untuk:\n"+
			"• Saldo provider di bawah batas minimum\n"+
			"• Transaksi stuck di processing > %d menit\n\n"+
			"⏰ Pengecekan setiap 5 menit\n"+
			"🕐 Waktu: %s",
		settings.StuckMinutes, now,
	)

	return client.SendMessageMarkdown(settings.ChatID, msg)
}

// -----------------------------------------------------------------------
// Main alert check — called by scheduler every 5 minutes
// -----------------------------------------------------------------------

func (s *telegramAlertService) RunAlertCheck(ctx context.Context) error {
	settings, err := s.GetSettings()
	if err != nil {
		return fmt.Errorf("gagal memuat settings telegram: %w", err)
	}
	if !settings.Enabled || settings.BotToken == "" || settings.ChatID == "" {
		return nil // silently skip
	}

	client := telegram.NewClient(settings.BotToken)

	// 1. Check provider balances
	s.checkProviderBalances(client, settings)

	// 2. Check stuck transactions
	s.checkStuckTransactions(client, settings)

	return nil
}

// -----------------------------------------------------------------------
// Balance alert
// -----------------------------------------------------------------------

func (s *telegramAlertService) checkProviderBalances(client *telegram.Client, settings *TelegramSettings) {
	if len(settings.MinBalance) == 0 {
		return
	}

	providers, err := s.providerRepo.List()
	if err != nil {
		log.Printf("[TelegramAlert] Gagal memuat daftar provider: %v", err)
		return
	}

	wib := time.FixedZone("WIB", 7*3600)

	for _, prov := range providers {
		if !prov.IsActive {
			continue
		}

		threshold, exists := settings.MinBalance[strings.ToUpper(prov.Code)]
		if !exists || threshold <= 0 {
			continue
		}

		if prov.Balance >= threshold {
			continue
		}

		// Cooldown: no repeat for same provider within 15 minutes
		alertKey := fmt.Sprintf("balance_%s", prov.Code)
		if s.isCooldown(alertKey, 15*time.Minute) {
			continue
		}

		now := time.Now().In(wib).Format("02/01/2006 15:04:05 WIB")
		balanceStr := formatBalanceAlert(prov.Balance, prov.Code)
		thresholdStr := formatBalanceAlert(threshold, prov.Code)

		msg := fmt.Sprintf(
			"⚠️ *SALDO RENDAH*\n\n"+
				"Provider: *%s* (`%s`)\n"+
				"Saldo: %s\n"+
				"Batas Minimum: %s\n"+
				"Waktu: %s\n\n"+
				"Segera lakukan top-up saldo provider!",
			prov.Name, prov.Code, balanceStr, thresholdStr, now,
		)

		if err := client.SendMessageMarkdown(settings.ChatID, msg); err != nil {
			log.Printf("[TelegramAlert] Gagal kirim alert saldo %s: %v", prov.Code, err)
		} else {
			s.setCooldown(alertKey)
			log.Printf("[TelegramAlert] Alert saldo rendah terkirim: %s (saldo: %.0f, threshold: %.0f)", prov.Code, prov.Balance, threshold)
		}
	}
}

// -----------------------------------------------------------------------
// Stuck transaction alert
// -----------------------------------------------------------------------

func (s *telegramAlertService) checkStuckTransactions(client *telegram.Client, settings *TelegramSettings) {
	stuckMinutes := settings.StuckMinutes
	if stuckMinutes <= 0 {
		stuckMinutes = 5
	}

	// Cooldown: only alert stuck transactions every 5 minutes
	alertKey := "stuck_transactions"
	if s.isCooldown(alertKey, 5*time.Minute) {
		return
	}

	minAge := time.Duration(stuckMinutes) * time.Minute
	txs, err := s.txRepo.FindProcessingOlderThan(minAge, 50)
	if err != nil {
		log.Printf("[TelegramAlert] Gagal query transaksi stuck: %v", err)
		return
	}

	if len(txs) == 0 {
		return
	}

	wib := time.FixedZone("WIB", 7*3600)
	now := time.Now()

	var lines []string
	for i, tx := range txs {
		if i >= 20 { // Limit to 20 items to avoid message too long
			lines = append(lines, fmt.Sprintf("... dan %d transaksi lainnya", len(txs)-20))
			break
		}

		duration := int(now.Sub(tx.UpdatedAt).Minutes())
		providerName := "Unknown"
		if tx.Provider != nil {
			providerName = tx.Provider.Name
		}

		lines = append(lines, fmt.Sprintf(
			"• `%s` | %s | %d menit",
			tx.InvoiceNumber, providerName, duration,
		))
	}

	msg := fmt.Sprintf(
		"🚨 *TRANSAKSI STUCK*\n\n"+
			"Ditemukan *%d* transaksi stuck di PROCESSING > %d menit:\n\n"+
			"%s\n\n"+
			"🕐 %s\n"+
			"Segera cek di Admin Panel!",
		len(txs), stuckMinutes, strings.Join(lines, "\n"),
		now.In(wib).Format("02/01/2006 15:04:05 WIB"),
	)

	if err := client.SendMessageMarkdown(settings.ChatID, msg); err != nil {
		log.Printf("[TelegramAlert] Gagal kirim alert transaksi stuck: %v", err)
	} else {
		s.setCooldown(alertKey)
		log.Printf("[TelegramAlert] Alert transaksi stuck terkirim: %d transaksi", len(txs))
	}
}

// -----------------------------------------------------------------------
// Cooldown helpers
// -----------------------------------------------------------------------

func (s *telegramAlertService) isCooldown(key string, duration time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lastSent, ok := s.lastAlerts[key]; ok {
		return time.Since(lastSent) < duration
	}
	return false
}

func (s *telegramAlertService) setCooldown(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAlerts[key] = time.Now()
}

// -----------------------------------------------------------------------
// Formatting helpers
// -----------------------------------------------------------------------

func formatBalanceAlert(balance float64, providerCode string) string {
	code := strings.ToUpper(providerCode)
	if code == "KIOSGAMER" {
		return fmt.Sprintf("%s Shell", humanizeNumber(balance))
	}
	return fmt.Sprintf("Rp %s", humanizeNumber(balance))
}

func humanizeNumber(f float64) string {
	n := int64(math.Round(f))
	if n == 0 {
		return "0"
	}

	negative := n < 0
	if negative {
		n = -n
	}

	s := strconv.FormatInt(n, 10)
	parts := make([]string, 0, (len(s)/3)+1)

	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)

	result := strings.Join(parts, ".")
	if negative {
		result = "-" + result
	}
	return result
}
