
package kiosgamer

import (
    "testing"
)

func TestResolveAppID(t *testing.T) {
	tests := []struct {
		name string
		slug string
		want int
	}{
		{"Free Fire", "free-fire", 100067},
		{"Call of Duty Mobile", "codm", 100082},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (&kiosgamerService{}).resolveAppID(tt.slug)
			if got != tt.want {
				t.Fatalf("resolveAppID(%q) = %d, want %d",
					tt.slug, got, tt.want)
			}
		})
	}
}

// Jalankan diagnostik integrasi secara manual hanya jika
// tersedia credential dan repository yang valid.
// Jangan membuat order atau memanggil endpoint pembayaran.

