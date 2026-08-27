package sse

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// Event adalah struktur data yang dikirim ke browser via SSE.
type Event struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// ClientChan adalah channel per-koneksi browser.
type ClientChan chan string

// Hub mengelola semua koneksi SSE yang sedang aktif, dikelompokkan per invoice.
type Hub struct {
	mu      sync.RWMutex
	clients map[string][]ClientChan
}

// GlobalHub adalah instance SSE Hub tunggal yang digunakan seluruh aplikasi.
var GlobalHub = NewHub()

// NewHub membuat Hub baru.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[string][]ClientChan),
	}
}

// Register mendaftarkan koneksi client baru untuk invoice tertentu.
func (h *Hub) Register(invoice string, ch ClientChan) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[invoice] = append(h.clients[invoice], ch)
	log.Printf("[SSE] Client connected for invoice: %s (total: %d)", invoice, len(h.clients[invoice]))
}

// Unregister melepas koneksi client dari invoice tertentu.
func (h *Hub) Unregister(invoice string, ch ClientChan) {
	h.mu.Lock()
	defer h.mu.Unlock()

	list := h.clients[invoice]
	newList := make([]ClientChan, 0, len(list))
	for _, c := range list {
		if c != ch {
			newList = append(newList, c)
		}
	}

	if len(newList) == 0 {
		delete(h.clients, invoice)
	} else {
		h.clients[invoice] = newList
	}
	log.Printf("[SSE] Client disconnected for invoice: %s (remaining: %d)", invoice, len(newList))
}

// Broadcast mengirim event ke semua client yang sedang menonton invoice tsb.
func (h *Hub) Broadcast(invoice string, eventType string, payload interface{}) {
	h.mu.RLock()
	list := h.clients[invoice]
	h.mu.RUnlock()

	if len(list) == 0 {
		return
	}

	evt := Event{Type: eventType, Payload: payload}
	data, err := json.Marshal(evt)
	if err != nil {
		log.Printf("[SSE] Marshal error for invoice %s: %v", invoice, err)
		return
	}

	msg := "data: " + string(data) + "\n\n"
	for _, ch := range list {
		select {
		case ch <- msg:
		case <-time.After(200 * time.Millisecond):
			log.Printf("[SSE] Skipping slow client for invoice: %s", invoice)
		}
	}
}

// ClientCount mengembalikan jumlah client aktif untuk invoice tertentu.
func (h *Hub) ClientCount(invoice string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[invoice])
}