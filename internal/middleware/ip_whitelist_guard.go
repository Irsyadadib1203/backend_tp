package middleware

import (
	"bytes"
	"io"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

func IPWhitelistGuard(ipService service.IPWhitelistService) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		var userIDPtr *uint
		if uidVal, exists := c.Get("user_id"); exists {
			if uid, ok := uidVal.(uint); ok {
				userIDPtr = &uid
			}
		}

		allowed, reason := ipService.ValidateIP(clientIP, userIDPtr)
		if !allowed {
			// Read body for logging if available
			var bodyBytes []byte
			if c.Request.Body != nil {
				bodyBytes, _ = io.ReadAll(c.Request.Body)
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}

			ipService.RecordAccess(
				clientIP,
				c.Request.URL.Path,
				c.Request.Method,
				403,
				"BLOCKED",
				reason,
				c.Request.UserAgent(),
				string(bodyBytes),
			)

			response.Forbidden(c, "Akses ditolak: IP Anda ("+clientIP+") belum terdaftar dalam whitelist atau sedang diblokir.")
			c.Abort()
			return
		}

		c.Next()
	}
}
