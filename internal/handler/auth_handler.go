package handler

import (
	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

type AuthHandler struct {
	authService service.AuthService
}
type Verify2FARequest struct {
	TempToken string `json:"temp_token" binding:"required"`
	Code      string `json:"code" binding:"required,len=6"`
}
type CodeRequest struct {
	Code string `json:"code" binding:"required,len=6"`
}
type Disable2FARequest struct {
	Password string `json:"password" binding:"required"`
	Code     string `json:"code" binding:"required,len=6"`
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
	res, err := h.authService.Login(req.Email, req.Password)
	if err != nil {
		response.Unauthorized(c, err.Error())
		return
	}
	if res.Requires2FA {
		response.Success(c, "Verifikasi 2FA diperlukan", gin.H{
			"requires_2fa": true,
			"temp_token":   res.TempToken,
		})
		return
	}
	response.Success(c, "Login successful", gin.H{"user": res.User, "token": res.Token})
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

func (h *AuthHandler) Verify2FA(c *gin.Context) {
	var req Verify2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Kode harus 6 digit", err.Error())
		return
	}
	user, token, err := h.authService.Verify2FALogin(req.TempToken, req.Code)
	if err != nil {
		response.Unauthorized(c, err.Error())
		return
	}
	response.Success(c, "Login successful", gin.H{"user": user, "token": token})
}

func (h *AuthHandler) Setup2FA(c *gin.Context) {
	uid, _ := c.Get("user_id")
	secret, err := h.authService.Setup2FA(uid.(uint))
	if err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}
	response.Success(c, "Secret dibuat", gin.H{"secret": secret})
}

func (h *AuthHandler) Enable2FA(c *gin.Context) {
	var req CodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Kode harus 6 digit", err.Error())
		return
	}
	uid, _ := c.Get("user_id")
	if err := h.authService.Enable2FA(uid.(uint), req.Code); err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}
	response.Success(c, "2FA berhasil diaktifkan", nil)
}

func (h *AuthHandler) Disable2FA(c *gin.Context) {
	var req Disable2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Input tidak valid", err.Error())
		return
	}
	uid, _ := c.Get("user_id")
	if err := h.authService.Disable2FA(uid.(uint), req.Password, req.Code); err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}
	response.Success(c, "2FA dinonaktifkan", nil)
}