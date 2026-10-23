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

// PriceChangeItem represents a single SKU price change event.
type PriceChangeItem struct {
	Category    string
	ProductCode string
	OldPrice    float64
	NewPrice    float64
}

// TelegramAlertService handles periodic Telegram alert checks for
// low provider balances, stuck transactions, and SKU price changes.
type TelegramAlertService interface {
	// RunAlertCheck is called by the scheduler every N minutes.
	RunAlertCheck(ctx context.Context) error
	// GetSettings returns the current Telegram configuration.
	GetSettings() (*TelegramSettings, error)
	// SaveSettings persists updated Telegram configuration.
	SaveSettings(settings *TelegramSettings) error
	// SendTestMessage sends a test notification to validate the bot setup.
	SendTestMessage() error
	// SendPriceChangeAlert notifies about SKU cost price changes from a provider.
	SendPriceChangeAlert(providerName, providerCode string, changes []PriceChangeItem) error
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
	// Track providers currently below minimum balance to detect recovery
	isLowBalance map[string]bool
	mu           sync.Mutex
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
		isLowBalance: make(map[string]bool),
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
	s.checkProviderBalances(ctx, client, settings)

	// 2. Check stuck transactions
	s.checkStuckTransactions(client, settings)

	return nil
}

// -----------------------------------------------------------------------
// Balance alert
// -----------------------------------------------------------------------

func (s *telegramAlertService) checkProviderBalances(ctx context.Context, client *telegram.Client, settings *TelegramSettings) {
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

		code := strings.ToUpper(prov.Code)
		threshold, exists := settings.MinBalance[code]
		if !exists || threshold <= 0 {
			continue
		}

		// Sinkronisasi saldo realtime ke adapter jika tersedia di registry
		currentBalance := prov.Balance
		if s.registry != nil {
			if adapter, ok := s.registry.Get(code); ok {
				syncCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				if liveBal, err := adapter.Balance(syncCtx); err == nil {
					currentBalance = liveBal
					if store, ok := s.providerRepo.(interface {
						UpdateBalanceSnapshot(id uint, balance float64, checkedAt time.Time) error
					}); ok {
						_ = store.UpdateBalanceSnapshot(prov.ID, liveBal, time.Now())
					} else {
						_ = s.providerRepo.UpdateBalance(prov.ID, liveBal)
					}
				}
				cancel()
			}
		}

		alertKey := fmt.Sprintf("balance_%s", code)

		// KONDISI 1: Saldo sudah di atas atau sama dengan batas minimum
		if currentBalance >= threshold {
			s.mu.Lock()
			wasLow := s.isLowBalance[code]
			s.isLowBalance[code] = false
			s.mu.Unlock()

			// Jika sebelumnya dalam kondisi saldo rendah, kirim pesan konfirmasi bahwa saldo sudah aman dan trigger berhenti!
			if wasLow {
				now := time.Now().In(wib).Format("02/01/2006 15:04:05 WIB")
				balanceStr := formatBalanceAlert(currentBalance, code)
				thresholdStr := formatBalanceAlert(threshold, code)

				msg := fmt.Sprintf(
					"✅ *SALDO PROVIDER SUDAH AMAN*\n\n"+
						"Provider: *%s* (`%s`)\n"+
						"Saldo Sekarang: *%s*\n"+
						"Batas Minimum: %s\n"+
						"Waktu: %s\n\n"+
						"Saldo sudah berada di atas batas minimum. Trigger notifikasi Telegram dihentikan.",
					prov.Name, code, balanceStr, thresholdStr, now,
				)

				if err := client.SendMessageMarkdown(settings.ChatID, msg); err != nil {
					log.Printf("[TelegramAlert] Gagal kirim recovery saldo %s: %v", code, err)
				} else {
					log.Printf("[TelegramAlert] Notifikasi saldo pulih terkirim: %s (saldo: %.0f >= threshold: %.0f)", code, currentBalance, threshold)
				}
			}
			continue
		}

		// KONDISI 2: Saldo di bawah batas minimum (< threshold)
		// Trigger aktif terus setiap interval 5 menit sampai saldo diisi di atas minimum
		s.mu.Lock()
		s.isLowBalance[code] = true
		s.mu.Unlock()

		// Cooldown 4 menit: mencegah double-firing dalam interval yang sama,
		// namun menjamin trigger terkirim setiap putaran scheduler 5 menit
		if s.isCooldown(alertKey, 4*time.Minute) {
			continue
		}

		now := time.Now().In(wib).Format("02/01/2006 15:04:05 WIB")
		balanceStr := formatBalanceAlert(currentBalance, code)
		thresholdStr := formatBalanceAlert(threshold, code)

		msg := fmt.Sprintf(
			"⚠️ *PERINGATAN SALDO RENDAH*\n\n"+
				"Provider: *%s* (`%s`)\n"+
				"Saldo Saat Ini: *%s*\n"+
				"Batas Minimum: %s\n"+
				"Waktu: %s\n\n"+
				"⚠️ Saldo masih di bawah batas minimum!\n"+
				"Peringatan ini akan dikirim setiap 5 menit sampai saldo diisi di atas batas minimum.",
			prov.Name, code, balanceStr, thresholdStr, now,
		)

		if err := client.SendMessageMarkdown(settings.ChatID, msg); err != nil {
			log.Printf("[TelegramAlert] Gagal kirim alert saldo %s: %v", code, err)
		} else {
			s.setCooldown(alertKey)
			log.Printf("[TelegramAlert] Alert saldo rendah terkirim: %s (saldo: %.0f, threshold: %.0f)", code, currentBalance, threshold)
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

	// Cooldown 4 menit agar berjalan konsisten setiap putaran 5 menit
	alertKey := "stuck_transactions"
	if s.isCooldown(alertKey, 4*time.Minute) {
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

// -----------------------------------------------------------------------
// Price change alerts
// -----------------------------------------------------------------------

func (s *telegramAlertService) SendPriceChangeAlert(providerName, providerCode string, changes []PriceChangeItem) error {
	if len(changes) == 0 {
		return nil
	}

	settings, err := s.GetSettings()
	if err != nil {
		return fmt.Errorf("gagal memuat settings telegram: %w", err)
	}
	if !settings.Enabled || settings.BotToken == "" || settings.ChatID == "" {
		return nil // skip if bot not enabled
	}

	client := telegram.NewClient(settings.BotToken)

	// Group changes by Category preserving insertion order
	categories := make([]string, 0)
	catMap := make(map[string][]PriceChangeItem)

	for _, c := range changes {
		cat := strings.TrimSpace(c.Category)
		if cat == "" {
			cat = "Umum"
		}
		if _, exists := catMap[cat]; !exists {
			categories = append(categories, cat)
		}
		catMap[cat] = append(catMap[cat], c)
	}

	// Send notification for each category
	for _, cat := range categories {
		items := catMap[cat]
		if len(items) == 0 {
			continue
		}

		// Chunk into max 25 items per message to avoid exceeding Telegram message size
		chunkSize := 25
		for i := 0; i < len(items); i += chunkSize {
			end := i + chunkSize
			if end > len(items) {
				end = len(items)
			}
			chunk := items[i:end]

			var lines []string
			lines = append(lines, fmt.Sprintf("Provider : %s", providerName))
			lines = append(lines, fmt.Sprintf("Kategori : %s", cat))
			lines = append(lines, "")

			for _, item := range chunk {
				oldStr := formatPriceChange(item.OldPrice, providerCode)
				newStr := formatPriceChange(item.NewPrice, providerCode)
				lines = append(lines, fmt.Sprintf("- %s = %s -> %s", item.ProductCode, oldStr, newStr))
			}

			msg := strings.Join(lines, "\n")
			if err := client.SendMessagePlain(settings.ChatID, msg); err != nil {
				log.Printf("[TelegramAlert] Gagal kirim notifikasi perubahan harga (%s - %s): %v", providerName, cat, err)
			} else {
				log.Printf("[TelegramAlert] Notifikasi perubahan harga terkirim (%s - %s: %d SKU)", providerName, cat, len(chunk))
			}
			time.Sleep(150 * time.Millisecond) // brief pause between messages
		}
	}

	return nil
}

func formatPriceChange(price float64, providerCode string) string {
	code := strings.ToUpper(providerCode)
	if code == "KIOSGAMER" {
		return fmt.Sprintf("%s Shell", humanizeNumber(price))
	}
	return fmt.Sprintf("Rp. %s", humanizeNumber(price))
}
