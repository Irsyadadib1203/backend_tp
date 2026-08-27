package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/service"
)

type DigiflazzSellerHandler struct {
	sellerService service.DigiflazzSellerService
}

func NewDigiflazzSellerHandler(sellerService service.DigiflazzSellerService) *DigiflazzSellerHandler {
	return &DigiflazzSellerHandler{sellerService: sellerService}
}

func (h *DigiflazzSellerHandler) GetPriceList(c *gin.Context) {
	var req service.SellerPriceListRequest
	_ = c.ShouldBindJSON(&req)

	products, err := h.sellerService.GetPriceList(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"data": []string{},
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": products,
	})
}

func (h *DigiflazzSellerHandler) CreateTransaction(c *gin.Context) {
	var req service.SellerTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"data": gin.H{
				"ref_id":  req.RefID,
				"message": "Invalid request parameter: " + err.Error(),
				"status":  "Gagal",
				"rc":      "40",
			},
		})
		return
	}

	clientIP := c.ClientIP()
	result, err := h.sellerService.ProcessH2HTransaction(&req, clientIP)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"data": result,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": result,
	})
}

func (h *DigiflazzSellerHandler) CheckStatus(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		RefID    string `json:"ref_id" binding:"required"`
		Sign     string `json:"sign" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"data": gin.H{
				"ref_id":  req.RefID,
				"message": "Invalid request parameters",
				"status":  "Gagal",
				"rc":      "40",
			},
		})
		return
	}

	result, err := h.sellerService.CheckH2HStatus(req.Username, req.RefID, req.Sign)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"ref_id":  req.RefID,
				"message": err.Error(),
				"status":  "Gagal",
				"rc":      "40",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": result,
	})
}

func (h *DigiflazzSellerHandler) CheckBalance(c *gin.Context) {
	var req service.SellerCheckBalanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"data": gin.H{
				"message": "Invalid request parameters",
			},
		})
		return
	}

	balance, err := h.sellerService.CheckH2HBalance(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"deposit": balance,
		},
	})
}
