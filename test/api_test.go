package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"topup-backend/config"
	"topup-backend/internal/database"
	"topup-backend/internal/domain"
	"topup-backend/internal/handler"
	"topup-backend/internal/middleware"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/repository"
	"topup-backend/internal/service"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		AppEnv:         "test",
		DBDriver:       "sqlite",
		DBSQLite:       ":memory:",
		JWTSecret:      "test-secret-key-1234",
		JWTExpiryHours: 24,
	}

	db := database.InitDB(cfg)
	database.SeedInitialData(db)

	// Seed test game & nominal for test assertions
	testGame := domain.Game{
		Name:     "Mobile Legends",
		Slug:     "mobile-legends",
		Category: domain.CategoryGames,
		IsActive: true,
	}
	db.Create(&testGame)

	testNominal := domain.Nominal{
		GameID:              testGame.ID,
		ProviderID:          1,
		Name:                "86 Diamonds",
		BasePrice:           18000,
		PricePublic:         20000,
		PriceMember:         19500,
		PriceReseller:       19000,
		PriceVIP:            18500,
		ProviderProductCode: "ML86",
		SellerProductCode:   "ML86",
		IsActive:            true,
	}
	db.Create(&testNominal)

	userRepo := repository.NewUserRepository(db)
	gameRepo := repository.NewGameRepository(db)
	nominalRepo := repository.NewNominalRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	depositRepo := repository.NewDepositRepository(db)
	ipRepo := repository.NewIPRepository(db)
	providerRepo := repository.NewProviderRepository(db)
	paymentRepo := repository.NewPaymentRepository(db)
	bannerRepo := repository.NewBannerRepository(db)
	articleRepo := repository.NewArticleRepository(db)

	authService := service.NewAuthService(userRepo, cfg)
	nicknameService := service.NewNicknameService()
	digiflazzBuyerService := service.NewDigiflazzBuyerService(providerRepo, cfg)
	webhookService := service.NewWebhookService(providerRepo)
	digiflazzSellerService := service.NewDigiflazzSellerService(userRepo, nominalRepo, txRepo, digiflazzBuyerService, webhookService)
	gameService := service.NewGameService(gameRepo, nominalRepo, providerRepo, digiflazzBuyerService)
	txService := service.NewTransactionService(txRepo, nominalRepo, gameRepo, userRepo, paymentRepo, digiflazzBuyerService)
	depositService := service.NewDepositService(depositRepo, userRepo)
	ipService := service.NewIPWhitelistService(ipRepo)

	authHandler := handler.NewAuthHandler(authService)
	gameHandler := handler.NewGameHandler(gameService, nicknameService)
	txHandler := handler.NewTransactionHandler(txService, authService)
	digiflazzBuyerHandler := handler.NewDigiflazzBuyerHandler(digiflazzBuyerService, txService)
	digiflazzSellerHandler := handler.NewDigiflazzSellerHandler(digiflazzSellerService)
	adminHandler := handler.NewAdminHandler(gameService, txService, depositService, digiflazzBuyerService, userRepo, providerRepo, paymentRepo, bannerRepo, articleRepo, nil)
	ipHandler := handler.NewIPWhitelistHandler(ipService)

	r := gin.New()
	r.Use(middleware.SetupCORS(cfg))

	api := r.Group("/api/v1")
	{
		auth := api.Group("/auth")
		{
			auth.POST("/register", authHandler.Register)
			auth.POST("/login", authHandler.Login)
		}

		api.GET("/games", gameHandler.GetGames)
		api.GET("/games/:slug", gameHandler.GetGameBySlug)
		api.POST("/games/check-nickname", gameHandler.CheckNickname)
		api.POST("/transactions", txHandler.CreateOrder)
		api.GET("/transactions/:invoice", txHandler.GetByInvoice)

		// H2H (Protected by IP Whitelist)
		h2h := api.Group("/h2h", middleware.IPWhitelistGuard(ipService))
		{
			h2h.POST("/price-list", digiflazzSellerHandler.GetPriceList)
			h2h.POST("/transaction", digiflazzSellerHandler.CreateTransaction)
			h2h.POST("/check-status", digiflazzSellerHandler.CheckStatus)
			h2h.POST("/check-balance", digiflazzSellerHandler.CheckBalance)
		}

		// Callback
		api.POST("/callback/digiflazz", digiflazzBuyerHandler.HandleCallback)

		// Admin
		admin := api.Group("/admin", middleware.AuthMiddleware(authService), middleware.RequireRole(domain.RoleAdmin, domain.RoleSuperAdmin))
		{
			admin.GET("/dashboard/stats", adminHandler.GetDashboardStats)
			admin.GET("/games", adminHandler.GetGames)
			admin.GET("/nominals", adminHandler.GetNominals)
			admin.GET("/ip-whitelist", ipHandler.GetWhitelists)
		}
	}

	return r
}

func TestPublicGamesAPI(t *testing.T) {
	r := setupTestRouter()

	req, _ := http.NewRequest("GET", "/api/v1/games", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool          `json:"success"`
		Data    []interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.Success || len(resp.Data) == 0 {
		t.Fatalf("Failed to parse games response or games list is empty: %v", err)
	}
}

func TestNicknameCheckerAPI(t *testing.T) {
	r := setupTestRouter()

	payload := map[string]string{
		"game_code": "MOBILE_LEGENDS",
		"user_id":   "12345678",
		"server_id": "1234",
	}
	jsonBody, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", "/api/v1/games/check-nickname", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
		t.Fatalf("Unexpected response code: %d", w.Code)
	}
}

func TestAdminAuthAndStats(t *testing.T) {
	r := setupTestRouter()

	// 1. Login with seeded admin credentials
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "admin@topup.com",
		"password": "admin123",
	})
	req, _ := http.NewRequest("POST", "/api/v1/auth/login", bytes.NewBuffer(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Admin login failed: %d, body: %s", w.Code, w.Body.String())
	}

	var loginResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
	token := loginResp.Data.Token

	// 2. Fetch Dashboard Stats
	statReq, _ := http.NewRequest("GET", "/api/v1/admin/dashboard/stats", nil)
	statReq.Header.Set("Authorization", "Bearer "+token)
	statW := httptest.NewRecorder()
	r.ServeHTTP(statW, statReq)

	if statW.Code != http.StatusOK {
		t.Fatalf("Failed to fetch admin stats with token: %d", statW.Code)
	}
}

func TestDigiflazzSellerPriceList(t *testing.T) {
	r := setupTestRouter()

	req, _ := http.NewRequest("POST", "/api/v1/h2h/price-list", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for H2H pricelist, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data []map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) == 0 {
		t.Fatalf("Expected non-empty H2H price list")
	}
}

func TestUnauthorizedIPBlocking(t *testing.T) {
	r := setupTestRouter()

	req, _ := http.NewRequest("POST", "/api/v1/h2h/price-list", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.100.99:54321" // Non-whitelisted IP
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("Expected status 403 Forbidden for unlisted IP, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSignatureHelper(t *testing.T) {
	username := "user123"
	key := "key123"
	refID := "INV123"
	expected := crypto.MD5Hash(username + key + refID)
	calculated := crypto.MD5Hash("user123key123INV123")
	if expected != calculated {
		t.Fatalf("Signature mismatch: %s vs %s", expected, calculated)
	}
}