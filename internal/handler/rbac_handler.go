package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

type RBACHandler struct {
	rbacService service.RBACService
}

func NewRBACHandler(rbacService service.RBACService) *RBACHandler {
	return &RBACHandler{rbacService: rbacService}
}

// GetRolePermissions returns the full permissions matrix (Admin & Operator)
func (h *RBACHandler) GetRolePermissions(c *gin.Context) {
	matrix, err := h.rbacService.GetAllRolePermissions()
	if err != nil {
		response.InternalServerError(c, "Gagal memuat matriks hak akses", err)
		return
	}

	response.Success(c, "Matriks hak akses berhasil dimuat", gin.H{
		"resources": domain.AllResources,
		"matrix":    matrix,
	})
}

type updatePermissionsRequest struct {
	Permissions []domain.RolePermission `json:"permissions" binding:"required"`
}

// UpdateRolePermissions saves the updated permissions matrix
func (h *RBACHandler) UpdateRolePermissions(c *gin.Context) {
	var req updatePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid permissions payload", err.Error())
		return
	}

	if err := h.rbacService.UpdateRolePermissions(req.Permissions); err != nil {
		response.InternalServerError(c, "Gagal memperbarui hak akses role", err)
		return
	}

	response.Success(c, "Hak akses role berhasil diperbarui", nil)
}

// GetMyPermissions returns the list of allowed resources for the currently authenticated admin/staff user
func (h *RBACHandler) GetMyPermissions(c *gin.Context) {
	roleVal, exists := c.Get("user_role")
	if !exists {
		response.Error(c, http.StatusUnauthorized, "User role not found")
		return
	}

	userRole, ok := roleVal.(domain.Role)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "Invalid user role type")
		return
	}

	allowed, err := h.rbacService.GetMyPermissions(userRole)
	if err != nil {
		response.InternalServerError(c, "Gagal memuat daftar izin pengguna", err)
		return
	}

	response.Success(c, "Izin pengguna berhasil dimuat", gin.H{
		"role":        userRole,
		"permissions": allowed,
	})
}
