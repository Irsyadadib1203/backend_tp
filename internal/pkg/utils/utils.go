package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// GenerateInvoiceNumber creates a unique invoice string like INV-20260824-A1B2C3
func GenerateInvoiceNumber() string {
	dateStr := time.Now().Format("20060102")
	randomPart := GenerateRandomString(6)
	return fmt.Sprintf("INV-%s-%s", dateStr, strings.ToUpper(randomPart))
}

// GenerateRefID creates a unique provider reference ID
func GenerateRefID() string {
	return fmt.Sprintf("REF%d%s", time.Now().UnixNano()%10000000, GenerateRandomString(4))
}

// GenerateAPIKey generates a 32 character API key
func GenerateAPIKey() string {
	return "top_" + strings.ToLower(GenerateRandomString(28))
}

// GenerateAPISecret generates a 48 character API secret
func GenerateAPISecret() string {
	return strings.ToLower(GenerateRandomString(48))
}

// GenerateRandomString generates random alphanumeric string of length n
func GenerateRandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			result[i] = letters[i%len(letters)]
		} else {
			result[i] = letters[num.Int64()]
		}
	}
	return string(result)
}

// Slugify converts string to URL-friendly slug
func Slugify(text string) string {
	text = strings.ToLower(text)
	reg := regexp.MustCompile("[^a-z0-9]+")
	text = reg.ReplaceAllString(text, "-")
	return strings.Trim(text, "-")
}
