package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

type DepositHandler struct {
	depositService service.DepositService
}

func NewDepositHandler(depositService service.DepositService) *DepositHandler {
	return &DepositHandler{depositService: depositService}
}

type CreateDepositRequest struct {
	Amount        float64 `json:"amount" binding:"required,min=10000"`
	PaymentMethod string  `json:"payment_method" binding:"required"`
}

func (h *DepositHandler) CreateDeposit(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		response.Unauthorized(c, "Login required")
		return
	}

	var req CreateDepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid deposit request data", err.Error())
		return
	}

	dep, err := h.depositService.CreateDeposit(userID.(uint), req.Amount, req.PaymentMethod)
	if err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	response.Created(c, "Deposit request created successfully", dep)
}

func (h *DepositHandler) GetUserDeposits(c *gin.Context) {
	userID, _ := c.Get("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	deps, total, err := h.depositService.GetUserDeposits(userID.(uint), offset, limit)
	if err != nil {
		response.InternalServerError(c, "Failed to retrieve deposits", err)
		return
	}

	response.Paginated(c, "Deposits retrieved", deps, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *DepositHandler) GetUserMutations(c *gin.Context) {
	userID, _ := c.Get("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset := (page - 1) * limit

	mutations, total, err := h.depositService.GetMutations(userID.(uint), offset, limit)
	if err != nil {
		response.InternalServerError(c, "Failed to retrieve balance mutations", err)
		return
	}

	response.Paginated(c, "Balance mutations retrieved", mutations, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}
