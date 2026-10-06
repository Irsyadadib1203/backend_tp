package otomaxhttp

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	statusPattern      = regexp.MustCompile(`(?i)\bstatus\s+([A-Z_]+)\b`)
	refIDPattern       = regexp.MustCompile(`(?i)\bRefId\s*:\s*([^\s.]+)`)
	balancePattern     = regexp.MustCompile(`(?i)\b(?:Sisa saldo|balance)\s+([0-9][0-9.,]*)`)
	transactionPattern = regexp.MustCompile(`\bR#([^\s]+)`)
	productPattern     = regexp.MustCompile(`\s([A-Za-z0-9_]+)\.([0-9]+)(?:[|(]([0-9]+)[)|])?,\s*status\b`)
	userPattern        = regexp.MustCompile(`^\s*([^.]*)\.\s*([^\s]+@[^\s]+)\.\s*plan\s+(.+?)\.\s*balance\s+([0-9][0-9.,]*)\s*$`)
)

func ParseResponse(raw []byte) (*ParsedResponse, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, fmt.Errorf("empty OtoMax response")
	}
	statusMatch := statusPattern.FindStringSubmatch(text)
	if len(statusMatch) < 2 {
		return nil, fmt.Errorf("OtoMax response has no status")
	}
	parsed := &ParsedResponse{Status: strings.ToUpper(statusMatch[1]), Raw: append([]byte(nil), raw...)}
	if match := transactionPattern.FindStringSubmatch(text); len(match) == 2 {
		parsed.TransactionID = match[1]
	}
	if match := productPattern.FindStringSubmatch(text); len(match) == 4 {
		parsed.ProductCode, parsed.CustomerID, parsed.ServerID = match[1], match[2], match[3]
	}
	if match := refIDPattern.FindStringSubmatch(text); len(match) == 2 {
		parsed.ProviderOrderID = match[1]
	}
	if match := balancePattern.FindStringSubmatch(text); len(match) == 2 {
		balance, err := parseNumber(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid OtoMax balance: %w", err)
		}
		parsed.Balance = balance
	}
	if idx := strings.Index(strings.ToLower(text), "refid:"); idx >= 0 {
		prefix := strings.TrimSpace(text[:idx])
		if statusEnd := statusPattern.FindStringIndex(prefix); statusEnd != nil {
			sn := strings.TrimSpace(prefix[statusEnd[1]:])
			parsed.SerialNumber = strings.Trim(strings.TrimSpace(sn), ". ")
		}
	}
	return parsed, nil
}

func ParseUser(raw []byte) (*UserInfo, error) {
	text := strings.TrimSpace(string(raw))
	match := userPattern.FindStringSubmatch(text)
	if len(match) != 5 {
		return nil, fmt.Errorf("invalid OtoMax user response")
	}
	balance, err := parseNumber(match[4])
	if err != nil {
		return nil, fmt.Errorf("invalid OtoMax user balance: %w", err)
	}
	return &UserInfo{Name: strings.TrimSpace(match[1]), Email: match[2], Plan: strings.TrimSpace(match[3]), Balance: balance, Raw: append([]byte(nil), raw...)}, nil
}

func parseNumber(value string) (float64, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(value), ".", "")
	cleaned = strings.ReplaceAll(cleaned, ",", ".")
	return strconv.ParseFloat(cleaned, 64)
}
