package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/pkg/sse"
	"topup-backend/internal/service"
)

type TransactionHandler struct {
	txService   service.TransactionService
	authService service.AuthService
}

func NewTransactionHandler(txService service.TransactionService, authService service.AuthService) *TransactionHandler {
	return &TransactionHandler{
		txService:   txService,
		authService: authService,
	}
}

func (h *TransactionHandler) CreateOrder(c *gin.Context) {
	var req service.CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid transaction request", err.Error())
		return
	}

	// 1. Extract from context if middleware was used
	if uidVal, exists := c.Get("user_id"); exists {
		if uid, ok := uidVal.(uint); ok && uid > 0 {
			req.UserID = &uid
		}
	}

	// 2. If UserID is not resolved, check Authorization header directly
	if req.UserID == nil || *req.UserID == 0 {
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" && h.authService != nil {
				if claims, err := h.authService.ValidateToken(parts[1]); err == nil && claims != nil {
					req.UserID = &claims.UserID
				}
			}
		}
	}

	tx, err := h.txService.CreateOrder(&req)
	if err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	response.Created(c, "Transaction order created successfully", tx)
}

func (h *TransactionHandler) GetByInvoice(c *gin.Context) {
	invoice := c.Param("invoice")
	tx, err := h.txService.GetByInvoice(invoice)
	if err != nil || tx == nil {
		response.NotFound(c, "Transaction not found")
		return
	}

	gameName := ""
	if tx.Game != nil {
		gameName = tx.Game.Name
	}
	nominalName := ""
	if tx.Nominal != nil {
		nominalName = tx.Nominal.Name
	}

	// 100% White-Label Sanitized Output (hides base_price, profit, provider internals)
	publicInvoice := gin.H{
		"id":                  tx.ID,
		"invoice_number":      tx.InvoiceNumber,
		"customer_id":         tx.CustomerID,
		"server_id":           tx.ServerID,
		"nickname":            tx.Nickname,
		"customer_phone":      tx.CustomerPhone,
		"game_name":           gameName,
		"nominal_name":        nominalName,
		"selling_price":       tx.SellingPrice,
		"admin_fee":           tx.AdminFee,
		"total_amount":        tx.TotalAmount,
		"status":              tx.Status,
		"payment_method":      tx.PaymentMethod,
		"payment_reference":   tx.PaymentReference,
		"payment_verified_at": tx.PaymentVerifiedAt,
		"created_at":          tx.CreatedAt,
		"completed_at":        tx.CompletedAt,
	}

	response.Success(c, "Transaction details retrieved", publicInvoice)
}

func (h *TransactionHandler) GetRecent(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "15")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 50 {
		limit = 15
	}

	txs, err := h.txService.ListRecent(limit)
	if err != nil {
		response.InternalServerError(c, "Failed to retrieve recent transactions", err)
		return
	}

	// Format minimal output for public display
	var formatted []gin.H
	for _, tx := range txs {
		gameName := "-"
		nominalName := "-"
		if tx.Game != nil {
			gameName = tx.Game.Name
		}
		if tx.Nominal != nil {
			nominalName = tx.Nominal.Name
		}

		formatted = append(formatted, gin.H{
			"id":             tx.ID,
			"invoice_number": tx.InvoiceNumber,
			"customer_id":    maskString(tx.CustomerID),
			"game_name":      gameName,
			"nominal_name":   nominalName,
			"total_amount":   tx.TotalAmount,
			"status":         tx.Status,
			"payment_method": tx.PaymentMethod,
			"created_at":     tx.CreatedAt,
		})
	}

	response.Success(c, "Recent transactions retrieved", formatted)
}

func (h *TransactionHandler) GetUserTransactions(c *gin.Context) {
	userID, _ := c.Get("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	txs, total, err := h.txService.ListUserTransactions(userID.(uint), offset, limit)
	if err != nil {
		response.InternalServerError(c, "Failed to retrieve user transactions", err)
		return
	}

	response.Paginated(c, "Transactions retrieved", txs, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

// StreamInvoice mengalirkan pembaruan status transaksi secara real-time via Server-Sent Events (SSE).
// Browser pembeli cukup koneksi 1x, status langsung berubah instan tanpa polling.
func (h *TransactionHandler) StreamInvoice(c *gin.Context) {
	invoice := c.Param("invoice")
	if invoice == "" {
		c.JSON(400, gin.H{"error": "invoice required"})
		return
	}

	// Pastikan transaksi ada sebelum buka stream
	tx, err := h.txService.GetByInvoice(invoice)
	if err != nil || tx == nil {
		c.JSON(404, gin.H{"error": "transaction not found"})
		return
	}

	// Set SSE headers — nginx butuh X-Accel-Buffering: no agar tidak buffer di proxy
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Access-Control-Allow-Origin", "*")

	// Buat channel untuk client ini
	clientChan := make(sse.ClientChan, 8)
	sse.GlobalHub.Register(invoice, clientChan)
	defer sse.GlobalHub.Unregister(invoice, clientChan)

	// Kirim status saat ini langsung sebagai event pertama (snapshot awal)
	initialPayload, _ := json.Marshal(gin.H{
		"status":       tx.Status,
		"invoice":      tx.InvoiceNumber,
		"completed_at": tx.CompletedAt,
	})
	fmt.Fprintf(c.Writer, "data: {\"type\":\"status_update\",\"payload\":%s}\n\n", initialPayload)
	c.Writer.Flush()

	// Jika sudah final, tutup stream langsung
	if tx.Status == "success" || tx.Status == "failed" || tx.Status == "refunded" {
		return
	}

	// Timeout otomatis 5 menit — browser akan reconnect sendiri via EventSource
	timeout := time.After(5 * time.Minute)
	// Heartbeat setiap 30 detik agar koneksi tidak di-kill nginx/firewall
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			fmt.Fprint(c.Writer, msg)
			c.Writer.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			c.Writer.Flush()
		case <-timeout:
			fmt.Fprintf(c.Writer, "data: {\"type\":\"timeout\"}\n\n")
			c.Writer.Flush()
			return
		case <-c.Request.Context().Done():
			// Browser menutup koneksi (navigasi pergi, tutup tab)
			return
		}
	}
}

func maskString(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}
