package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"topup-backend/config"
	"topup-backend/internal/database"
	"topup-backend/internal/domain"
	"topup-backend/internal/handler"
	"topup-backend/internal/middleware"
	"topup-backend/internal/pkg/scheduler"
	"topup-backend/internal/pkg/worker"
	"topup-backend/internal/repository"
	"topup-backend/internal/service"
)

func main() {
	// 1. Load Configuration
	cfg := config.LoadConfig()

	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 2. Initialize Database & Seeder
	db := database.InitDB(cfg)
	database.SeedInitialData(db)

	// 3. Initialize Repositories
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
	kiosgamerRepo := repository.NewKiosgamerRepository(db)
	rolePermRepo := repository.NewRolePermissionRepository(db)

	// 4. Initialize Services
	authService := service.NewAuthService(userRepo, cfg)
	nicknameService := service.NewNicknameService()
	digiflazzBuyerService := service.NewDigiflazzBuyerService(providerRepo, cfg)
	kiosgamerService := service.NewKiosgamerService(kiosgamerRepo, providerRepo, nominalRepo, gameRepo, cfg)
	webhookService := service.NewWebhookService(providerRepo)
	digiflazzSellerService := service.NewDigiflazzSellerService(userRepo, nominalRepo, txRepo, digiflazzBuyerService, webhookService)
	gameService := service.NewGameService(gameRepo, nominalRepo, providerRepo, digiflazzBuyerService)
	txService := service.NewTransactionService(txRepo, nominalRepo, gameRepo, userRepo, paymentRepo, providerRepo, digiflazzBuyerService, kiosgamerService)
	depositService := service.NewDepositService(depositRepo, userRepo)
	ipService := service.NewIPWhitelistService(ipRepo)
	rbacService := service.NewRBACService(rolePermRepo)

	// 5. Initialize Handlers
	authHandler := handler.NewAuthHandler(authService)
	gameHandler := handler.NewGameHandler(gameService, nicknameService)
	txHandler := handler.NewTransactionHandler(txService, authService)
	depositHandler := handler.NewDepositHandler(depositService)
	digiflazzBuyerHandler := handler.NewDigiflazzBuyerHandler(digiflazzBuyerService, txService)
	digiflazzSellerHandler := handler.NewDigiflazzSellerHandler(digiflazzSellerService)
	kiosgamerHandler := handler.NewKiosgamerHandler(kiosgamerService)
	rbacHandler := handler.NewRBACHandler(rbacService)
	tripayChannelService := service.NewTripayChannelService(cfg.TripayAPIKey, cfg.TripayBaseURL, paymentRepo)
	adminHandler := handler.NewAdminHandler(gameService, txService, depositService, digiflazzBuyerService, userRepo, providerRepo, paymentRepo, bannerRepo, articleRepo, tripayChannelService)
	ipHandler := handler.NewIPWhitelistHandler(ipService)
	paymentHandler := handler.NewPaymentHandler(txService, paymentRepo, cfg.TripayPrivateKey, cfg.GenericWebhookSecrets)

	// 5.1 Start Background Auto-Sync Scheduler (Sync prices & cut-off status every 1 hour)
	autoSyncScheduler := scheduler.NewAutoSyncScheduler(gameService, 1*time.Hour)
	autoSyncScheduler.Start()

	// 6. Router Setup
	r := gin.Default()
	r.MaxMultipartMemory = 8 << 20 // 8 MB

	// Apply Core Security & Performance Middlewares
	r.Use(middleware.SetupCORS(cfg))
	r.Use(middleware.SecurityHeaders())

	// Root health check
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"app":     "TopUp Game High Performance Backend Engine (Go)",
			"status":  "healthy",
			"version": "2.0.0",
		})
	})

	// Global Public Rate Limiter (30 req/sec, burst 60)
	publicLimiter := middleware.RateLimitMiddleware(rate.Limit(30), 60)
	// Strict Auth Rate Limiter (5 req/sec, burst 10)
	authLimiter := middleware.RateLimitMiddleware(rate.Limit(5), 10)
	// H2H Partner Rate Limiter (35 req/sec, burst 50)
	h2hLimiter := middleware.RateLimitMiddleware(rate.Limit(35), 50)

	// -------------------------------------------------------------
	// PUBLIC API (Storefront & Mobile / Next.js Public Frontend)
	// -------------------------------------------------------------
	api := r.Group("/api/v1", publicLimiter)
	{
		// Auth (Strict Rate Limited)
		auth := api.Group("/auth")
		{
			auth.POST("/register", authLimiter, authHandler.Register)
			auth.POST("/login", authLimiter, authHandler.Login)
			auth.GET("/me", middleware.AuthMiddleware(authService), authHandler.Me)
			auth.POST("/api-key", middleware.AuthMiddleware(authService), authHandler.GenerateAPIKey)
		}

		// Public Games & Nominals
		api.GET("/games", gameHandler.GetGames)
		api.POST("/games/check-nickname", gameHandler.CheckNickname)
		api.GET("/games/id/:id/nominals", gameHandler.GetNominalsByGame)
		api.GET("/check-user/:game", gameHandler.CheckUserFromQuery)
		api.GET("/games/:slug", gameHandler.GetGameBySlug)

		// Public Transactions & Invoices
		api.POST("/transactions", txHandler.CreateOrder)
		api.GET("/transactions/recent", txHandler.GetRecent)
		api.GET("/transactions/:invoice", txHandler.GetByInvoice)
		// SSE: Real-time invoice status stream (browser push, no polling needed)
		api.GET("/transactions/:invoice/stream", txHandler.StreamInvoice)

		// Payment Methods
		api.GET("/payment-methods", paymentHandler.GetPaymentMethods)

		// Public Banners & Articles
		api.GET("/banners", adminHandler.GetPublicBanners)
		api.GET("/articles", adminHandler.GetPublicArticles)

		// Member Authenticated Routes
		member := api.Group("/member", middleware.AuthMiddleware(authService))
		{
			member.GET("/transactions", txHandler.GetUserTransactions)
			member.POST("/deposits", depositHandler.CreateDeposit)
			member.GET("/deposits", depositHandler.GetUserDeposits)
			member.GET("/mutations", depositHandler.GetUserMutations)
		}

		// -------------------------------------------------------------
		// DIGIFLAZZ BUYER CALLBACK & PAYMENT WEBHOOKS
		// -------------------------------------------------------------
		api.POST("/callback/digiflazz", digiflazzBuyerHandler.HandleCallback)
		api.POST("/digiflazz/callback", digiflazzBuyerHandler.HandleCallback)
		api.POST("/callback/tripay", paymentHandler.HandleTripayCallback)
		api.POST("/callback/payment/:provider", paymentHandler.HandleGenericWebhook)

		// -------------------------------------------------------------
		// DIGIFLAZZ SELLER & OPEN API H2H (Protected by IP Whitelist + Rate Limiter)
		// -------------------------------------------------------------
		h2h := api.Group("/h2h", middleware.IPWhitelistGuard(ipService), h2hLimiter)
		{
			h2h.POST("/price-list", digiflazzSellerHandler.GetPriceList)
			h2h.POST("/transaction", digiflazzSellerHandler.CreateTransaction)
			h2h.POST("/check-status", digiflazzSellerHandler.CheckStatus)
			h2h.POST("/check-balance", digiflazzSellerHandler.CheckBalance)
		}
		// Top-level Digiflazz style endpoints
		v1Seller := r.Group("/v1", middleware.IPWhitelistGuard(ipService), h2hLimiter)
		{
			v1Seller.POST("/price-list", digiflazzSellerHandler.GetPriceList)
			v1Seller.POST("/transaction", digiflazzSellerHandler.CreateTransaction)
			v1Seller.POST("/cek-saldo", digiflazzSellerHandler.CheckBalance)
		}

		// -------------------------------------------------------------
		// ADMIN PANEL API (Protected by JWT RBAC: SuperAdmin / Admin / Operator)
		// -------------------------------------------------------------
		// ADMIN PANEL API (Protected by JWT RBAC: SuperAdmin / Admin / Operator)
		// -------------------------------------------------------------
		admin := api.Group("/admin", middleware.AuthMiddleware(authService), middleware.RequireRole(domain.RoleAdmin, domain.RoleSuperAdmin, domain.RoleOperator))
		{
			// RBAC User & Role Permissions
			admin.GET("/rbac/my-permissions", rbacHandler.GetMyPermissions)
			admin.GET("/rbac/permissions", middleware.RequireRole(domain.RoleSuperAdmin), rbacHandler.GetRolePermissions)
			admin.POST("/rbac/permissions", middleware.RequireRole(domain.RoleSuperAdmin), rbacHandler.UpdateRolePermissions)

			// Dashboard
			admin.GET("/dashboard/stats", middleware.RequirePermission(rbacService, domain.ResourceDashboard), adminHandler.GetDashboardStats)

			// Games
			games := admin.Group("/games", middleware.RequirePermission(rbacService, domain.ResourceGames))
			{
				games.GET("", adminHandler.GetGames)
				games.POST("", adminHandler.CreateGame)
				games.PUT("/:id", adminHandler.UpdateGame)
				games.DELETE("/:id", adminHandler.DeleteGame)
			}

			// Nominals & Pricing
			nominals := admin.Group("/nominals", middleware.RequirePermission(rbacService, domain.ResourceNominals))
			{
				nominals.GET("", adminHandler.GetNominals)
				nominals.POST("", adminHandler.CreateNominal)
				nominals.PUT("/:id", adminHandler.UpdateNominal)
				nominals.DELETE("/:id", adminHandler.DeleteNominal)
				nominals.POST("/batch-switch-provider", adminHandler.BatchSwitchProvider)
			}

			// Providers (Accessible if nominals or settings are allowed)
			admin.GET("/providers", adminHandler.GetProviders)

			// Digiflazz Sync & Balances
			digi := admin.Group("/digiflazz", middleware.RequirePermission(rbacService, domain.ResourceDigiflazz))
			{
				digi.POST("/sync", adminHandler.SyncDigiflazz)
				digi.GET("/balance", adminHandler.GetDigiflazzBalance)
			}

			// Kiosgamer Provider Management
			kios := admin.Group("/kiosgamer", middleware.RequirePermission(rbacService, domain.ResourceKiosgamer))
			{
				kios.GET("/status", kiosgamerHandler.Status)
				kios.POST("/credentials", kiosgamerHandler.SaveCredentials)
				kios.POST("/health-check", kiosgamerHandler.HealthCheck)
				kios.POST("/recover", kiosgamerHandler.Recover)
				kios.GET("/catalog", kiosgamerHandler.GetCatalog)
				kios.POST("/sync-catalog", kiosgamerHandler.AutoSyncCatalog)
				kios.PUT("/nominals/code", kiosgamerHandler.UpdateNominalCode)
			}

			// Transactions
			txs := admin.Group("/transactions", middleware.RequirePermission(rbacService, domain.ResourceTransactions))
			{
				txs.GET("", adminHandler.GetTransactions)
				txs.POST("/:id/retry", adminHandler.ManualRetryTx)
				txs.POST("/:id/success", adminHandler.ManualSuccessTx)
				txs.POST("/:id/refund", adminHandler.ManualRefundTx)
			}

			// IP Whitelist & Watchlist Management
			ip := admin.Group("", middleware.RequirePermission(rbacService, domain.ResourceIPWhitelist))
			{
				ip.GET("/ip-whitelist", ipHandler.GetWhitelists)
				ip.POST("/ip-whitelist", ipHandler.AddWhitelist)
				ip.DELETE("/ip-whitelist/:id", ipHandler.DeleteWhitelist)
				ip.GET("/ip-logs", ipHandler.GetAccessLogs)
				ip.GET("/watchlist", ipHandler.GetWatchlist)
				ip.POST("/watchlist/block", ipHandler.BlockIP)
				ip.POST("/watchlist/unblock", ipHandler.UnblockIP)
			}

			// Users & Resellers
			users := admin.Group("/users", middleware.RequirePermission(rbacService, domain.ResourceUsers))
			{
				users.GET("", adminHandler.GetUsers)
				users.POST("", adminHandler.CreateUser)
				users.PUT("/:id", adminHandler.UpdateUser)
			}

			// Deposits
			deposits := admin.Group("/deposits", middleware.RequirePermission(rbacService, domain.ResourceDeposits))
			{
				deposits.GET("", adminHandler.GetDeposits)
				deposits.POST("/:id/approve", adminHandler.ApproveDeposit)
				deposits.POST("/:id/reject", adminHandler.RejectDeposit)
			}

			// Payment Methods & Logs
			payments := admin.Group("/payment-methods", middleware.RequirePermission(rbacService, domain.ResourcePaymentMethods))
			{
				payments.GET("", adminHandler.GetPaymentMethods)
				payments.POST("", adminHandler.CreatePaymentMethod)
				payments.PUT("/:id", adminHandler.UpdatePaymentMethod)
				payments.DELETE("/:id", adminHandler.DeletePaymentMethod)
				payments.POST("/sync-tripay", adminHandler.SyncTripayPaymentMethods)
			}
			admin.GET("/webhooks/logs", middleware.RequirePermission(rbacService, domain.ResourceSettings), adminHandler.GetWebhookLogs)

			// Banners
			banners := admin.Group("/banners", middleware.RequirePermission(rbacService, domain.ResourceBanners))
			{
				banners.GET("", adminHandler.GetBanners)
				banners.POST("", adminHandler.CreateBanner)
				banners.PUT("/:id", adminHandler.UpdateBanner)
				banners.DELETE("/:id", adminHandler.DeleteBanner)
			}

			// Articles
			articles := admin.Group("/articles", middleware.RequirePermission(rbacService, domain.ResourceArticles))
			{
				articles.GET("", adminHandler.GetArticles)
				articles.POST("", adminHandler.CreateArticle)
				articles.PUT("/:id", adminHandler.UpdateArticle)
				articles.DELETE("/:id", adminHandler.DeleteArticle)
			}
		}
	}

	// -------------------------------------------------------------
	// 7. Resilient HTTP Server with Timeouts & Graceful Shutdown
	// -------------------------------------------------------------
	addr := fmt.Sprintf(":%s", cfg.AppPort)
	srv := &http.Server{
		Addr:           addr,
		Handler:        r,
		ReadTimeout:    15 * time.Second, // Protect against Slowloris DDoS
		WriteTimeout:   30 * time.Second, // Allow sufficient time for payment webhooks/queries
		IdleTimeout:    60 * time.Second, // Reuse TCP connections efficiently
		MaxHeaderBytes: 1 << 20,          // 1 MB header limit
	}

	// Run server in background goroutine
	go func() {
		log.Printf("[Server] Top-Up Engine running on %s (Env: %s)", addr, cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Server] Failed to listen: %v", err)
		}
	}()

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Server] Gracefully shutting down Top-Up Server...")

	// 15-second context timeout to finish ongoing requests
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[Server] Server forced to shutdown: %v", err)
	}

	// Stop background services gracefully
	autoSyncScheduler.Stop()
	worker.GlobalPool.Stop(10 * time.Second)

	log.Println("[Server] Server exited cleanly.")
}