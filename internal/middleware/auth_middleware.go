package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

func AuthMiddleware(authService service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Unauthorized(c, "Authorization header is required")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			response.Unauthorized(c, "Invalid authorization format. Must be Bearer <token>")
			c.Abort()
			return
		}

		tokenString := parts[1]
		claims, err := authService.ValidateToken(tokenString)
		if err != nil {
			response.Unauthorized(c, "Invalid or expired token")
			c.Abort()
			return
		}

		// Store user details in context
		c.Set("user_id", claims.UserID)
		c.Set("user_email", claims.Email)
		c.Set("user_role", claims.Role)
		c.Set("user_tier", claims.Tier)

		c.Next()
	}
}

func RequireRole(allowedRoles ...domain.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		roleVal, exists := c.Get("user_role")
		if !exists {
			response.Forbidden(c, "User role not identified")
			c.Abort()
			return
		}

		userRole, ok := roleVal.(domain.Role)
		if !ok {
			response.Forbidden(c, "Invalid user role type")
			c.Abort()
			return
		}

		// Superadmin always has access
		if userRole == domain.RoleSuperAdmin {
			c.Next()
			return
		}

		hasPermission := false
		for _, r := range allowedRoles {
			if userRole == r {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			response.Forbidden(c, "You do not have permission to access this resource")
			c.Abort()
			return
		}

		c.Next()
	}
}
