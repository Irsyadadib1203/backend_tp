package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/service"
)

type H2HHandler struct {
	h2hService service.H2HService
}

func NewH2HHandler(h2hService service.H2HService) *H2HHandler {
	return &H2HHandler{h2hService: h2hService}
}

// respondListError memetakan error service ke HTTP status untuk endpoint katalog.
func respondListError(c *gin.Context, err error) {
	switch {
	case service.IsH2HAuthError(err):
		c.JSON(http.StatusUnauthorized, gin.H{"data": []string{}, "message": err.Error()})
	case errors.Is(err, service.ErrH2HBrandNotFound):
		c.JSON(http.StatusNotFound, gin.H{"data": []string{}, "message": err.Error()})
	default:
		log.Printf("[H2H] internal error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"data": []string{}, "message": "Terjadi kesalahan pada server"})
	}
}

// POST /h2h/category  — daftar seluruh brand. sign = md5(apikey + "category")
func (h *H2HHandler) GetCategories(c *gin.Context) {
	var req service.H2HCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": []string{}, "message": "Invalid request parameters"})
		return
	}

	brands, err := h.h2hService.GetCategories(&req)
	if err != nil {
		respondListError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": brands})
}

// POST /h2h/price-list — seluruh produk. sign = md5(apikey + "pricelist")
func (h *H2HHandler) GetPriceList(c *gin.Context) {
	var req service.H2HPriceListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": []string{}, "message": "Invalid request parameters"})
		return
	}

	products, err := h.h2hService.GetPriceList(&req)
	if err != nil {
		respondListError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": products})
}

// POST /h2h/product — produk berdasarkan brand. sign = md5(apikey + "product")
func (h *H2HHandler) GetProductsByBrand(c *gin.Context) {
	var req service.H2HProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": []string{}, "message": "Invalid request parameters"})
		return
	}

	products, err := h.h2hService.GetProductsByBrand(&req)
	if err != nil {
		respondListError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": products})
}

// POST /h2h/transaction — sign = md5(apikey + ref_id)
func (h *H2HHandler) CreateTransaction(c *gin.Context) {
	var req service.H2HTransactionRequest
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

	// Gaya Digiflazz: error bisnis tetap HTTP 200 dengan status/rc di body.
	result, _ := h.h2hService.ProcessTransaction(&req, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// POST /h2h/check-status — sign = md5(apikey + ref_id)
func (h *H2HHandler) CheckStatus(c *gin.Context) {
	var req service.H2HCheckStatusRequest
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

	result, err := h.h2hService.CheckStatus(&req)
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
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// POST /h2h/check-balance — sign = md5(apikey + "balance")
func (h *H2HHandler) CheckBalance(c *gin.Context) {
	var req service.H2HCheckBalanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": gin.H{"message": "Invalid request parameters"}})
		return
	}

	balance, err := h.h2hService.CheckBalance(&req)
	if err != nil {
		status := http.StatusOK
		if service.IsH2HAuthError(err) {
			status = http.StatusUnauthorized
		}
		c.JSON(status, gin.H{"data": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"balance": balance}})
}