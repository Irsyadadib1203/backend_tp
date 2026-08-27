package handler

import (
	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

type AuthHandler struct {
	authService service.AuthService
}

func NewAuthHandler(authService service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

type RegisterRequest struct {
	Name        string `json:"name" binding:"required"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	PhoneNumber string `json:"phone_number"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type GenerateAPIKeyRequest struct {
	WebhookURL string `json:"webhook_url"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input data", err.Error())
		return
	}

	user, token, err := h.authService.Register(req.Name, req.Email, req.Password, req.PhoneNumber)
	if err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	response.Created(c, "Registration successful", gin.H{
		"user":  user,
		"token": token,
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid email or password format", err.Error())
		return
	}

	user, token, err := h.authService.Login(req.Email, req.Password)
	if err != nil {
		response.Unauthorized(c, err.Error())
		return
	}

	response.Success(c, "Login successful", gin.H{
		"user":  user,
		"token": token,
	})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	user, err := h.authService.GetProfile(userID.(uint))
	if err != nil {
		response.NotFound(c, "User not found")
		return
	}

	response.Success(c, "Profile retrieved successfully", user)
}

func (h *AuthHandler) GenerateAPIKey(c *gin.Context) {
	userID, _ := c.Get("user_id")
	var req GenerateAPIKeyRequest
	_ = c.ShouldBindJSON(&req)

	apiKey, err := h.authService.GenerateAPIKey(userID.(uint), req.WebhookURL)
	if err != nil {
		response.InternalServerError(c, "Failed to generate API Key", err)
		return
	}

	response.Success(c, "API Key generated successfully", apiKey)
}
