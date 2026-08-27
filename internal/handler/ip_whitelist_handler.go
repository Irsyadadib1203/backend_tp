package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

type IPWhitelistHandler struct {
	ipService service.IPWhitelistService
}

func NewIPWhitelistHandler(ipService service.IPWhitelistService) *IPWhitelistHandler {
	return &IPWhitelistHandler{ipService: ipService}
}

type AddIPRequest struct {
	IPAddress string `json:"ip_address" binding:"required"`
	Label     string `json:"label"`
	UserID    *uint  `json:"user_id"`
}

type BlockIPRequest struct {
	IPAddress string `json:"ip_address" binding:"required"`
	Reason    string `json:"reason"`
	Hours     int    `json:"hours"`
}

func (h *IPWhitelistHandler) GetWhitelists(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	var userID *uint
	if uidStr := c.Query("user_id"); uidStr != "" {
		if uid, err := strconv.Atoi(uidStr); err == nil {
			u := uint(uid)
			userID = &u
		}
	}
	offset := (page - 1) * limit

	items, total, err := h.ipService.ListWhitelists(offset, limit, userID)
	if err != nil {
		response.InternalServerError(c, "Failed to load IP whitelists", err)
		return
	}

	response.Paginated(c, "IP Whitelists loaded", items, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *IPWhitelistHandler) AddWhitelist(c *gin.Context) {
	var req AddIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input data", err.Error())
		return
	}

	userEmail, _ := c.Get("user_email")
	createdBy := "admin"
	if email, ok := userEmail.(string); ok && email != "" {
		createdBy = email
	}

	item, err := h.ipService.AddWhitelist(req.IPAddress, req.Label, req.UserID, createdBy)
	if err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	response.Created(c, "IP Whitelist added successfully", item)
}

func (h *IPWhitelistHandler) DeleteWhitelist(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.ipService.DeleteWhitelist(uint(id)); err != nil {
		response.InternalServerError(c, "Failed to delete IP", err)
		return
	}

	response.Success(c, "IP Whitelist deleted successfully", nil)
}

func (h *IPWhitelistHandler) GetAccessLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	status := c.Query("status")
	ip := c.Query("ip")
	offset := (page - 1) * limit

	logs, total, err := h.ipService.ListAccessLogs(offset, limit, status, ip)
	if err != nil {
		response.InternalServerError(c, "Failed to load access logs", err)
		return
	}

	response.Paginated(c, "Access logs loaded", logs, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *IPWhitelistHandler) GetWatchlist(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset := (page - 1) * limit

	items, total, err := h.ipService.ListWatchlist(offset, limit)
	if err != nil {
		response.InternalServerError(c, "Failed to load watchlist", err)
		return
	}

	response.Paginated(c, "Watchlist loaded", items, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *IPWhitelistHandler) BlockIP(c *gin.Context) {
	var req BlockIPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input data", err.Error())
		return
	}

	duration := 24 * time.Hour
	if req.Hours > 0 {
		duration = time.Duration(req.Hours) * time.Hour
	}

	if err := h.ipService.BlockIP(req.IPAddress, req.Reason, duration); err != nil {
		response.InternalServerError(c, "Failed to block IP", err)
		return
	}

	response.Success(c, "IP blocked successfully", nil)
}

func (h *IPWhitelistHandler) UnblockIP(c *gin.Context) {
	var req struct {
		IPAddress string `json:"ip_address" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input data", err.Error())
		return
	}

	if err := h.ipService.UnblockIP(req.IPAddress); err != nil {
		response.InternalServerError(c, "Failed to unblock IP", err)
		return
	}

	response.Success(c, "IP unblocked successfully", nil)
}
