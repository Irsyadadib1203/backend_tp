package handler

import (
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/pkg/response"
	"topup-backend/internal/repository"
	"topup-backend/internal/service"
)

type PaymentHandler struct {
	txService             service.TransactionService
	depositService        service.DepositService
	paymentRepo           repository.PaymentRepository
	tripayPrivateKey      string
	genericWebhookSecrets map[string]string
}

func NewPaymentHandler(
	txService service.TransactionService,
	depositService service.DepositService,
	paymentRepo repository.PaymentRepository,
	tripayPrivateKey string,
	genericWebhookSecrets map[string]string,
) *PaymentHandler {
	return &PaymentHandler{
		txService:             txService,
		depositService:        depositService,
		paymentRepo:           paymentRepo,
		tripayPrivateKey:      tripayPrivateKey,
		genericWebhookSecrets: genericWebhookSecrets,
	}
}

// verifyHMACSignature checks rawBody against HMAC-SHA256(rawBody, secret)
// using a constant-time comparison. Shared by every webhook route in this
// handler so all of them get the same fail-closed treatment.
func verifyHMACSignature(secret string, rawBody []byte, signatureHeader string) bool {
	if secret == "" || signatureHeader == "" {
		return false
	}

	expected := crypto.HMACSHA256(secret, string(rawBody))

	expectedBytes, err1 := hex.DecodeString(expected)
	receivedBytes, err2 := hex.DecodeString(signatureHeader)
	if err1 != nil || err2 != nil {
		return false
	}

	return hmac.Equal(expectedBytes, receivedBytes)
}

func (h *PaymentHandler) verifyTripaySignature(rawBody []byte, signatureHeader string) bool {
	return verifyHMACSignature(h.tripayPrivateKey, rawBody, signatureHeader)
}

func (h *PaymentHandler) GetPaymentMethods(c *gin.Context) {
	methods, err := h.paymentRepo.ListActive()
	if err != nil {
		response.InternalServerError(c, "Failed to load payment methods", err)
		return
	}

	response.Success(c, "Payment methods retrieved", methods)
}

func (h *PaymentHandler) HandleTripayCallback(c *gin.Context) {
	// Read raw body first — signature is computed over the exact raw bytes,
	// so it must be read before any JSON binding touches the body.
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Failed to read callback body"})
		return
	}

	signature := c.GetHeader("X-Callback-Signature")
	if !h.verifyTripaySignature(rawBody, signature) {
		log.Printf("[TripayCallback] REJECTED invalid signature from IP %s", c.ClientIP())
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Invalid signature"})
		return
	}

	// Tripay also sends an event header; only "payment_status" carries status updates.
	event := c.GetHeader("X-Callback-Event")
	if event != "" && event != "payment_status" {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Event ignored"})
		return
	}

	var payload struct {
		Reference       string  `json:"reference"`
		MerchantRef     string  `json:"merchant_ref"`
		Status          string  `json:"status"` // "PAID", "EXPIRED", "FAILED"
		TotalAmount     float64 `json:"total_amount"`
		IsClosedPayment int     `json:"is_closed_payment"`
	}

	if err := json.Unmarshal(rawBody, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid callback data"})
		return
	}

	if payload.MerchantRef == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Missing merchant_ref"})
		return
	}

	if payload.Status == "PAID" {
		if strings.HasPrefix(payload.MerchantRef, "DEP-") {
			if h.depositService != nil {
				if err := h.depositService.HandleTripayDepositSuccess(payload.MerchantRef, payload.Reference, payload.TotalAmount); err != nil {
					log.Printf("[TripayCallback] Deposit HandlePaymentSuccess error for ref %s: %v", payload.MerchantRef, err)
				}
			}
		} else {
			// txService.HandlePaymentSuccess must itself re-check the transaction's
			// expected amount against payload.TotalAmount and be idempotent
			// (Tripay may retry the same callback more than once).
			if err := h.txService.HandlePaymentSuccess(payload.MerchantRef, payload.Reference, payload.TotalAmount); err != nil {
				log.Printf("[TripayCallback] HandlePaymentSuccess error for ref %s: %v", payload.MerchantRef, err)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// HandleGenericWebhook is a fail-closed entry point for payment gateways
// other than Tripay. A provider is only trusted once an operator explicitly
// configures a secret for it (GENERIC_WEBHOOK_SECRETS env var) — with no
// secret configured, every request for that provider is rejected outright,
// regardless of what the payload claims.
func (h *PaymentHandler) HandleGenericWebhook(c *gin.Context) {
	provider := c.Param("provider")

	secret, configured := h.genericWebhookSecrets[strings.ToLower(provider)]
	if !configured {
		log.Printf("[GenericWebhook] REJECTED: no secret configured for provider %q (IP %s)", provider, c.ClientIP())
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "This payment provider is not configured to receive callbacks",
		})
		return
	}

	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Failed to read callback body"})
		return
	}

	signature := c.GetHeader("X-Callback-Signature")
	if !verifyHMACSignature(secret, rawBody, signature) {
		log.Printf("[GenericWebhook] REJECTED invalid signature for provider %q from IP %s", provider, c.ClientIP())
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Invalid signature"})
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid callback data"})
		return
	}

	invoiceNumber := ""
	if inv, ok := payload["merchant_ref"].(string); ok {
		invoiceNumber = inv
	} else if inv, ok := payload["invoice_number"].(string); ok {
		invoiceNumber = inv
	} else if inv, ok := payload["order_id"].(string); ok {
		invoiceNumber = inv
	}

	status := ""
	if st, ok := payload["status"].(string); ok {
		status = st
	}

	amount := 0.0
	if amt, ok := payload["total_amount"].(float64); ok {
		amount = amt
	} else if amt, ok := payload["amount"].(float64); ok {
		amount = amt
	}

	if (status == "PAID" || status == "SUCCESS" || status == "settlement") && invoiceNumber != "" {
		if err := h.txService.HandlePaymentSuccess(invoiceNumber, provider+"_PAYMENT", amount); err != nil {
			log.Printf("[GenericWebhook] HandlePaymentSuccess error for provider %q ref %s: %v", provider, invoiceNumber, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"provider": provider,
	})
}