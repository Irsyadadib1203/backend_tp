package service

import (
	"testing"
)

func TestHumanizeNumber(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1.000"},
		{50000, "50.000"},
		{500000, "500.000"},
		{1234567, "1.234.567"},
		{-50000, "-50.000"},
	}

	for _, tt := range tests {
		result := humanizeNumber(tt.input)
		if result != tt.expected {
			t.Errorf("humanizeNumber(%f) = %s, expected %s", tt.input, result, tt.expected)
		}
	}
}

func TestFormatBalanceAlert(t *testing.T) {
	tests := []struct {
		balance  float64
		code     string
		expected string
	}{
		{500000, "DIGIFLAZZ", "Rp 500.000"},
		{50, "KIOSGAMER", "50 Shell"},
		{1000000, "EXCELLENTSTORE", "Rp 1.000.000"},
	}

	for _, tt := range tests {
		result := formatBalanceAlert(tt.balance, tt.code)
		if result != tt.expected {
			t.Errorf("formatBalanceAlert(%f, %s) = %s, expected %s", tt.balance, tt.code, result, tt.expected)
		}
	}
}
