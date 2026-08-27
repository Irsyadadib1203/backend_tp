package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/service"
)

type DigiflazzBuyerHandler struct {
	digiflazzService service.DigiflazzBuyerService
	txService        service.TransactionService
}

func NewDigiflazzBuyerHandler(digiflazzService service.DigiflazzBuyerService, txService service.TransactionService) *DigiflazzBuyerHandler {
	return &DigiflazzBuyerHandler{
		digiflazzService: digiflazzService,
		txService:        txService,
	}
}

func (h *DigiflazzBuyerHandler) HandleCallback(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Failed to read body"})
		return
	}

	sigHeader := c.GetHeader("X-Hub-Signature")
	if sigHeader == "" {
		sigHeader = c.GetHeader("X-Digiflazz-Delivery")
	}

	payload, err := h.digiflazzService.ProcessCallback(bodyBytes, sigHeader)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := h.txService.HandleDigiflazzCallback(payload); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "Callback received but handling returned: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Digiflazz callback processed successfully",
		"ref_id":  payload.Data.RefID,
		"status":  payload.Data.Status,
	})
}
