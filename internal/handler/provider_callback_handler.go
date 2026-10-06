package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
	"topup-backend/internal/service"
)

type ProviderCallbackHandler struct {
	txService        service.TransactionService
	providerRegistry *provider.Registry
	providerRepo     repository.ProviderRepository
}

func NewProviderCallbackHandler(
	txService service.TransactionService,
	providerRegistry *provider.Registry,
	providerRepo repository.ProviderRepository,
) *ProviderCallbackHandler {
	return &ProviderCallbackHandler{
		txService:        txService,
		providerRegistry: providerRegistry,
		providerRepo:     providerRepo,
	}
}

// HandleCallback handles generic callbacks for any registered provider (§5 Fase 4).
// Route: GET/POST /api/callback/provider/:code (and /api/v1/callback/provider/:code)
func (h *ProviderCallbackHandler) HandleCallback(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "provider code is required"})
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Failed to read body"})
		return
	}
	// Restore body so the adapter CallbackParser can read it
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 1. Log incoming callback to WebhookLog after masking secrets/credentials (§5 requirement 4, §8 rule 5)
	if h.providerRepo != nil {
		logPayload := bodyBytes
		if len(logPayload) == 0 && c.Request.URL.RawQuery != "" {
			logPayload = []byte(c.Request.URL.RawQuery)
		}
		maskedPayload := MaskSensitiveWebhookData(logPayload)
		_ = h.providerRepo.LogWebhook(&domain.WebhookLog{
			Direction:    domain.WebhookIncoming,
			ProviderName: strings.ToUpper(code) + "_CALLBACK",
			Payload:      maskedPayload,
			CreatedAt:    time.Now(),
		})
	}

	// 2. Process callback through service and registry (§3.4, §5)
	tx, result, err := h.txService.HandleProviderCallback(code, c.Request)
	if err != nil {
		errStr := err.Error()
		lowerErr := strings.ToLower(errStr)
		// Signature validation, empty data, or unsupported provider errors return HTTP 400
		if strings.Contains(lowerErr, "signature") ||
			strings.Contains(lowerErr, "empty callback") ||
			strings.Contains(lowerErr, "not found in registry") ||
			strings.Contains(lowerErr, "does not support callbacks") ||
			strings.Contains(lowerErr, "invalid json") {
			c.JSON(http.StatusBadRequest, gin.H{"message": errStr})
			return
		}

		// After signature is validated, processing errors (e.g. transaction not found)
		// return HTTP 200 acknowledged with legacy format (§4.4)
		c.JSON(http.StatusOK, gin.H{"message": "Callback received but handling returned: " + errStr})
		return
	}

	providerTitle := strings.ToUpper(code)
	if strings.EqualFold(code, "DIGIFLAZZ") {
		providerTitle = "Digiflazz"
	}

	refID := ""
	if tx != nil {
		refID = tx.RefID
	}
	status := ""
	if result != nil {
		status = result.ProviderStatus
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("%s callback processed successfully", providerTitle),
		"ref_id":  refID,
		"status":  status,
	})
}

// MaskSensitiveWebhookData masks sensitive credentials, signatures, and tokens in JSON payloads (§8 rule 5).
func MaskSensitiveWebhookData(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		if values, queryErr := url.ParseQuery(string(raw)); queryErr == nil && len(values) > 0 {
			for key := range values {
				if isSensitiveKey(key) {
					values.Set(key, "***MASKED***")
				}
			}
			return values.Encode()
		}
		return string(raw)
	}
	maskSensitiveMap(data)
	maskedBytes, err := json.Marshal(data)
	if err != nil {
		return string(raw)
	}
	return string(maskedBytes)
}

func maskSensitiveMap(m map[string]interface{}) {
	for k, v := range m {
		if isSensitiveKey(k) {
			m[k] = "***MASKED***"
			continue
		}
		if childMap, ok := v.(map[string]interface{}); ok {
			maskSensitiveMap(childMap)
		} else if slice, ok := v.([]interface{}); ok {
			for _, item := range slice {
				if itemMap, ok := item.(map[string]interface{}); ok {
					maskSensitiveMap(itemMap)
				}
			}
		}
	}
}

func isSensitiveKey(key string) bool {
	lowerK := strings.ToLower(key)
	return strings.Contains(lowerK, "sign") ||
		strings.Contains(lowerK, "secret") ||
		strings.Contains(lowerK, "token") ||
		strings.Contains(lowerK, "apikey") ||
		strings.Contains(lowerK, "api_key") ||
		strings.Contains(lowerK, "password") ||
		strings.Contains(lowerK, "credential") ||
		strings.Contains(lowerK, "cookie") ||
		strings.Contains(lowerK, "session")
}
