package middleware

import (
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"topup-backend/config"
)

func SetupCORS(cfg *config.Config) gin.HandlerFunc {
	// Base allowed origins
	allowedOrigins := []string{
		"http://localhost:3000",
		"http://localhost:3001",
		"http://127.0.0.1:3000",
		"http://127.0.0.1:3001",
	}

	if cfg != nil {
		if cfg.FrontendURL != "" {
			for _, u := range strings.Split(cfg.FrontendURL, ",") {
				trimmed := strings.TrimSpace(u)
				if trimmed != "" {
					allowedOrigins = append(allowedOrigins, trimmed)
				}
			}
		}
		if cfg.AdminURL != "" {
			for _, u := range strings.Split(cfg.AdminURL, ",") {
				trimmed := strings.TrimSpace(u)
				if trimmed != "" {
					allowedOrigins = append(allowedOrigins, trimmed)
				}
			}
		}
	}

	corsConfig := cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "X-Callback-Signature", "X-API-Key", "X-Sign"},
		ExposeHeaders:    []string{"Content-Length", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	// In development or if explicitly allowed, fallback to AllowOriginFunc to allow flexibility
	if cfg != nil && cfg.AppEnv == "development" {
		corsConfig.AllowOriginFunc = func(origin string) bool {
			return true // Allow all local/dev origins
		}
	}

	return cors.New(corsConfig)
}

