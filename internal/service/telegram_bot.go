package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"topup-backend/internal/pkg/telegram"
)

var reCN = regexp.MustCompile(`^/cn(?:@\w+)?\s+(\d+)\s*[\s(]\s*(\d+)\)?\s*$`)

type TelegramBot struct {
	alert   TelegramAlertService
	checker *NickChecker
}

func NewTelegramBot(alert TelegramAlertService, checker *NickChecker) *TelegramBot {
	return &TelegramBot{alert: alert, checker: checker}
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

// Run dipanggil sebagai goroutine dari main.go: go bot.Run(ctx)
func (b *TelegramBot) Run(ctx context.Context) {
	var offset int64
	httpc := &http.Client{Timeout: 40 * time.Second}

	for ctx.Err() == nil {
		settings, err := b.alert.GetSettings()
		if err != nil || !settings.Enabled || settings.BotToken == "" {
			time.Sleep(10 * time.Second)
			continue
		}

		u := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d",
			settings.BotToken, offset)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := httpc.Do(req)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		var out struct {
			OK     bool       `json:"ok"`
			Result []tgUpdate `json:"result"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()

		for _, up := range out.Result {
			offset = up.UpdateID + 1
			if up.Message == nil {
				continue
			}
			go b.handle(ctx, settings, up)
		}
	}
}

func (b *TelegramBot) handle(ctx context.Context, s *TelegramSettings, up tgUpdate) {
	text := strings.TrimSpace(up.Message.Text)
	if !strings.HasPrefix(text, "/cn") {
		return
	}
	chatID := strconv.FormatInt(up.Message.Chat.ID, 10)

	// Hanya chat yang diizinkan di Admin Panel.
	if chatID != s.ChatID {
		return
	}

	client := telegram.NewClient(s.BotToken)

	m := reCN.FindStringSubmatch(text)
	if m == nil {
		_ = client.SendMessagePlain(chatID, "Format: /cn <id> <server>\nContoh: /cn 123456 1234")
		return
	}

	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	res, err := b.checker.Check(cctx, m[1], m[2])
	if err != nil {
		_ = client.SendMessagePlain(chatID, "❌ "+err.Error())
		return
	}
	if err := client.SendMessageMarkdown(chatID, res.Format()); err != nil {
		log.Printf("[CekNick] gagal kirim balasan: %v", err)
	}
}