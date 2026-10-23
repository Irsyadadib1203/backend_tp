package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Client is a lightweight Telegram Bot API client that uses only the
// standard library. It sends messages via the Bot API sendMessage endpoint.
type Client struct {
	botToken   string
	httpClient *http.Client
}

// NewClient creates a new Telegram Bot API client.
func NewClient(botToken string) *Client {
	return &Client{
		botToken: botToken,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// IsConfigured returns true if the bot token has been set.
func (c *Client) IsConfigured() bool {
	return c.botToken != ""
}

type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
}

// SendMessage sends a message with HTML parse mode.
func (c *Client) SendMessage(chatID, text string) error {
	return c.send(chatID, text, "HTML")
}

// SendMessageMarkdown sends a message with Markdown parse mode.
func (c *Client) SendMessageMarkdown(chatID, text string) error {
	return c.send(chatID, text, "Markdown")
}

// SendMessagePlain sends a message without any parse mode (plain text).
func (c *Client) SendMessagePlain(chatID, text string) error {
	return c.send(chatID, text, "")
}

func (c *Client) send(chatID, text, parseMode string) error {
	if !c.IsConfigured() {
		return fmt.Errorf("telegram bot token belum dikonfigurasi")
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.botToken)

	payload := sendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: parseMode,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("kirim pesan telegram: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var tgResp telegramResponse
		_ = json.Unmarshal(respBody, &tgResp)
		log.Printf("[Telegram] Error %d: %s", resp.StatusCode, tgResp.Description)
		return fmt.Errorf("telegram API error %d: %s", resp.StatusCode, tgResp.Description)
	}

	return nil
}
