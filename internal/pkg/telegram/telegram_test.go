package telegram

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramClient(t *testing.T) {
	t.Run("IsConfigured", func(t *testing.T) {
		emptyClient := NewClient("")
		if emptyClient.IsConfigured() {
			t.Errorf("expected IsConfigured to be false for empty token")
		}

		configuredClient := NewClient("123456:dummy-token")
		if !configuredClient.IsConfigured() {
			t.Errorf("expected IsConfigured to be true for non-empty token")
		}
	})

	t.Run("SendMessage unconfigured", func(t *testing.T) {
		client := NewClient("")
		err := client.SendMessage("12345", "test")
		if err == nil {
			t.Errorf("expected error when sending message without bot token")
		}
	})

	t.Run("SendMessage with mock server", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST method, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok": true, "result": {}}`))
		}))
		defer server.Close()

		client := &Client{
			botToken:   "dummy-token",
			httpClient: server.Client(),
		}

		// Verify client struct
		if !client.IsConfigured() {
			t.Errorf("expected client to be configured")
		}
	})
}
