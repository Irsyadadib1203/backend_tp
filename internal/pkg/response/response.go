package response

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"topup-backend/config"
)

type StandardResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Meta    interface{} `json:"meta,omitempty"`
	Errors  interface{} `json:"errors,omitempty"`
}

func Success(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, StandardResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

func Created(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusCreated, StandardResponse{
		Success: true,
		Message: message,
		Data:    data,
	})
}

func Paginated(c *gin.Context, message string, data interface{}, meta interface{}) {
	c.JSON(http.StatusOK, StandardResponse{
		Success: true,
		Message: message,
		Data:    data,
		Meta:    meta,
	})
}

func BadRequest(c *gin.Context, message string, errors interface{}) {
	c.JSON(http.StatusBadRequest, StandardResponse{
		Success: false,
		Message: message,
		Errors:  errors,
	})
}

func Unauthorized(c *gin.Context, message string) {
	c.JSON(http.StatusUnauthorized, StandardResponse{
		Success: false,
		Message: message,
	})
}

func Forbidden(c *gin.Context, message string) {
	c.JSON(http.StatusForbidden, StandardResponse{
		Success: false,
		Message: message,
	})
}

func NotFound(c *gin.Context, message string) {
	c.JSON(http.StatusNotFound, StandardResponse{
		Success: false,
		Message: message,
	})
}

// InternalServerError always logs the full error server-side (so it's still
// debuggable), but only reflects the raw error text back to the client
// outside production. In production the client only ever sees the generic
// `message` — raw error strings can leak table/column names, query
// fragments, file paths, or other internals that help an attacker.
func InternalServerError(c *gin.Context, message string, err error) {
	if err != nil {
		log.Printf("[InternalServerError] %s: %v", message, err)
	}

	isProduction := config.AppConfig != nil && config.AppConfig.AppEnv == "production"

	var errDetails interface{}
	if err != nil && !isProduction {
		errDetails = err.Error()
	}

	c.JSON(http.StatusInternalServerError, StandardResponse{
		Success: false,
		Message: message,
		Errors:  errDetails,
	})
}

func Error(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, StandardResponse{
		Success: false,
		Message: message,
	})
}