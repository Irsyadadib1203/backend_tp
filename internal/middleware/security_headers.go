package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders applies essential HTTP security headers to protect against common web vulnerabilities
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Prevent clickjacking
		c.Header("X-Frame-Options", "SAMEORIGIN")
		// Prevent MIME-sniffing
		c.Header("X-Content-Type-Options", "nosniff")
		// XSS Filter
		c.Header("X-XSS-Protection", "1; mode=block")
		// Referrer policy
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// Permissions Policy
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		
		c.Next()
	}
}
