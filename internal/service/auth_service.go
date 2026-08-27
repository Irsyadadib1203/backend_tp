package service

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"topup-backend/config"
	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/pkg/utils"
	"topup-backend/internal/repository"
)

type JWTClaims struct {
	UserID uint        `json:"user_id"`
	Email  string      `json:"email"`
	Role   domain.Role `json:"role"`
	Tier   domain.Tier `json:"tier"`
	jwt.RegisteredClaims
}

type AuthService interface {
	Register(name, email, password, phone string) (*domain.User, string, error)
	Login(email, password string) (*domain.User, string, error)
	GetProfile(userID uint) (*domain.User, error)
	GenerateAPIKey(userID uint, webhookURL string) (*domain.APIKey, error)
	ValidateToken(tokenString string) (*JWTClaims, error)
}

type authService struct {
	userRepo repository.UserRepository
	cfg      *config.Config
}

func NewAuthService(userRepo repository.UserRepository, cfg *config.Config) AuthService {
	return &authService{userRepo: userRepo, cfg: cfg}
}

func (s *authService) Register(name, email, password, phone string) (*domain.User, string, error) {
	existing, _ := s.userRepo.FindByEmail(email)
	if existing != nil {
		return nil, "", errors.New("email already registered")
	}

	hashedPassword, err := crypto.HashPassword(password)
	if err != nil {
		return nil, "", err
	}

	user := &domain.User{
		Name:        name,
		Email:       email,
		Password:    hashedPassword,
		PhoneNumber: phone,
		Role:        domain.RoleMember,
		Tier:        domain.TierMember,
		Balance:     0,
		IsActive:    true,
	}

	if err := s.userRepo.Create(user); err != nil {
		return nil, "", err
	}

	token, err := s.generateJWT(user)
	if err != nil {
		return nil, "", err
	}

	return user, token, nil
}

func (s *authService) Login(email, password string) (*domain.User, string, error) {
	user, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return nil, "", errors.New("invalid email or password")
	}

	if !user.IsActive {
		return nil, "", errors.New("user account is inactive")
	}

	if !crypto.CheckPasswordHash(password, user.Password) {
		return nil, "", errors.New("invalid email or password")
	}

	token, err := s.generateJWT(user)
	if err != nil {
		return nil, "", err
	}

	return user, token, nil
}

func (s *authService) GetProfile(userID uint) (*domain.User, error) {
	return s.userRepo.FindByID(userID)
}

func (s *authService) GenerateAPIKey(userID uint, webhookURL string) (*domain.APIKey, error) {
	key := utils.GenerateAPIKey()
	secret := utils.GenerateAPISecret()

	existingKey, err := s.userRepo.GetAPIKeyByUserID(userID)
	if err == nil && existingKey != nil {
		existingKey.Key = key
		existingKey.Secret = secret
		existingKey.WebhookURL = webhookURL
		existingKey.IsActive = true
		if err := s.userRepo.CreateAPIKey(existingKey); err != nil {
			return nil, err
		}
		return existingKey, nil
	}

	apiKey := &domain.APIKey{
		UserID:     userID,
		Key:        key,
		Secret:     secret,
		WebhookURL: webhookURL,
		IsActive:   true,
	}
	if err := s.userRepo.CreateAPIKey(apiKey); err != nil {
		return nil, err
	}
	return apiKey, nil
}

func (s *authService) generateJWT(user *domain.User) (string, error) {
	expirationTime := time.Now().Add(time.Duration(s.cfg.JWTExpiryHours) * time.Hour)
	claims := &JWTClaims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
		Tier:   user.Tier,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "topup-backend",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

func (s *authService) ValidateToken(tokenString string) (*JWTClaims, error) {
	claims := &JWTClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.cfg.JWTSecret), nil
	})

	if err != nil || !token.Valid {
		return nil, errors.New("invalid or expired token")
	}

	return claims, nil
}
