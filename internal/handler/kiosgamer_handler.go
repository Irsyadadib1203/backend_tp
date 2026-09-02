package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

type KiosgamerHandler struct {
	service service.KiosgamerService
}

func NewKiosgamerHandler(service service.KiosgamerService) *KiosgamerHandler {
	return &KiosgamerHandler{service: service}
}

type kiosgamerCredentialRequest struct {
	SessionKey string `json:"session_key"`
	TOTPSecret string `json:"totp_secret"`
}

func (h *KiosgamerHandler) SaveCredentials(c *gin.Context) {
	var req kiosgamerCredentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid Kiosgamer credential payload", err.Error())
		return
	}
	if req.SessionKey == "" && req.TOTPSecret == "" {
		response.BadRequest(c, "session_key or totp_secret is required", nil)
		return
	}
	if err := h.service.SaveCredentials(req.SessionKey, req.TOTPSecret); err != nil {
		response.InternalServerError(c, "Failed to save Kiosgamer credentials", err)
		return
	}
	response.Success(c, "Kiosgamer credentials saved securely", nil)
}

func (h *KiosgamerHandler) Status(c *gin.Context) {
	status, err := h.service.Status(c.Request.Context())
	if err != nil {
		response.InternalServerError(c, "Failed to load Kiosgamer status", err)
		return
	}
	response.Success(c, "Kiosgamer status loaded", status)
}

func (h *KiosgamerHandler) HealthCheck(c *gin.Context) {
	info, err := h.service.HealthCheck(c.Request.Context())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrKiosgamerNotConfigured):
			response.Error(c, http.StatusServiceUnavailable, "Kiosgamer is not configured")
		case errors.Is(err, service.ErrKiosgamerChallengeRequired):
			response.Error(c, http.StatusServiceUnavailable, "Kiosgamer requires browser challenge verification")
		case errors.Is(err, service.ErrKiosgamerSessionExpired):
			response.BadRequest(c, "Kiosgamer session expired: silakan perbarui session_key", nil)
		default:
			response.InternalServerError(c, "Kiosgamer health check failed", err)
		}
		return
	}
	response.Success(c, "Kiosgamer session is active", info)
}

func (h *KiosgamerHandler) Recover(c *gin.Context) {
	if err := h.service.RecoverSession(c.Request.Context()); err != nil {
		switch {
		case errors.Is(err, service.ErrKiosgamerReauthRequired):
			response.BadRequest(c, "Garena SSO session sudah kadaluarsa; silakan update session_key secara manual", nil)
		case errors.Is(err, service.ErrKiosgamerChallengeRequired):
			response.Error(c, http.StatusServiceUnavailable, "Kiosgamer memerlukan verifikasi bot/challenge")
		default:
			response.InternalServerError(c, "Kiosgamer session recovery failed: "+err.Error(), err)
		}
		return
	}
	response.Success(c, "Kiosgamer session recovered successfully", nil)
}

func (h *KiosgamerHandler) GetCatalog(c *gin.Context) {
	gameSlug := c.Query("game_slug")
	if gameSlug == "" {
		gameSlug = "free-fire"
	}
	items, err := h.service.FetchCatalog(c.Request.Context(), gameSlug)
	if err != nil {
		response.InternalServerError(c, "Gagal mengambil katalog produk dari Kiosgamer", err)
		return
	}
	response.Success(c, "Katalog Kiosgamer berhasil dimuat", items)
}

type autoSyncCatalogRequest struct {
	GameID   uint   `json:"game_id" binding:"required"`
	GameSlug string `json:"game_slug" binding:"required"`
}

func (h *KiosgamerHandler) AutoSyncCatalog(c *gin.Context) {
	var req autoSyncCatalogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: game_id and game_slug are required", err.Error())
		return
	}

	result, err := h.service.AutoSyncMapping(c.Request.Context(), req.GameID, req.GameSlug)
	if err != nil {
		response.InternalServerError(c, "Gagal melakukan auto-sync mapping SKU Kiosgamer", err)
		return
	}
	response.Success(c, result.Message, result)
}

type updateNominalCodeRequest struct {
	NominalID            uint   `json:"nominal_id" binding:"required"`
	KiosgamerProductCode string `json:"kiosgamer_product_code"`
}

func (h *KiosgamerHandler) UpdateNominalCode(c *gin.Context) {
	var req updateNominalCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid payload", err.Error())
		return
	}

	if err := h.service.UpdateNominalKiosgamerCode(req.NominalID, req.KiosgamerProductCode); err != nil {
		response.InternalServerError(c, "Gagal memperbarui SKU Kiosgamer nominal", err)
		return
	}
	response.Success(c, "SKU Kiosgamer nominal berhasil diperbarui", nil)
}
