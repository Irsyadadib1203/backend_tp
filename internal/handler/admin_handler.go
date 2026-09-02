package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/pkg/response"
	"topup-backend/internal/repository"
	"topup-backend/internal/service"
)

type AdminHandler struct {
	gameService          service.GameService
	txService            service.TransactionService
	depositService       service.DepositService
	digiflazzBuyer       service.DigiflazzBuyerService
	userRepo             repository.UserRepository
	providerRepo         repository.ProviderRepository
	paymentRepo          repository.PaymentRepository
	bannerRepo           repository.BannerRepository
	articleRepo          repository.ArticleRepository
	tripayChannelService service.TripayChannelService
}

func NewAdminHandler(
	gameService service.GameService,
	txService service.TransactionService,
	depositService service.DepositService,
	digiflazzBuyer service.DigiflazzBuyerService,
	userRepo repository.UserRepository,
	providerRepo repository.ProviderRepository,
	paymentRepo repository.PaymentRepository,
	bannerRepo repository.BannerRepository,
	articleRepo repository.ArticleRepository,
	tripayChannelService service.TripayChannelService,
) *AdminHandler {
	return &AdminHandler{
		gameService:          gameService,
		txService:            txService,
		depositService:       depositService,
		digiflazzBuyer:       digiflazzBuyer,
		userRepo:             userRepo,
		providerRepo:         providerRepo,
		paymentRepo:          paymentRepo,
		bannerRepo:           bannerRepo,
		articleRepo:          articleRepo,
		tripayChannelService: tripayChannelService,
	}
}



func (h *AdminHandler) GetDashboardStats(c *gin.Context) {
	stats, err := h.txService.GetDashboardStats()
	if err != nil {
		response.InternalServerError(c, "Failed to load dashboard stats", err)
		return
	}

	// Digiflazz balance
	digiBalance, _ := h.digiflazzBuyer.CheckBalance()
	stats["digiflazz_balance"] = digiBalance

	response.Success(c, "Dashboard stats loaded", stats)
}

// ----------------- GAMES -----------------

func (h *AdminHandler) GetGames(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	search := c.Query("search")
	category := c.Query("category")
	offset := (page - 1) * limit

	games, total, err := h.gameService.ListGamesAdmin(offset, limit, search, category)
	if err != nil {
		response.InternalServerError(c, "Failed to load games", err)
		return
	}

	response.Paginated(c, "Games loaded", games, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *AdminHandler) CreateGame(c *gin.Context) {
	var game domain.Game
	if err := c.ShouldBindJSON(&game); err != nil {
		response.BadRequest(c, "Invalid game data", err.Error())
		return
	}

	if err := h.gameService.CreateGame(&game); err != nil {
		response.InternalServerError(c, "Failed to create game", err)
		return
	}

	response.Created(c, "Game created successfully", game)
}

func (h *AdminHandler) UpdateGame(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var game domain.Game
	if err := c.ShouldBindJSON(&game); err != nil {
		response.BadRequest(c, "Invalid game data", err.Error())
		return
	}

	game.ID = uint(id)
	if err := h.gameService.UpdateGame(&game); err != nil {
		response.InternalServerError(c, "Failed to update game", err)
		return
	}

	response.Success(c, "Game updated successfully", game)
}

func (h *AdminHandler) DeleteGame(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.gameService.DeleteGame(uint(id)); err != nil {
		response.InternalServerError(c, "Failed to delete game", err)
		return
	}

	response.Success(c, "Game deleted successfully", nil)
}

// ----------------- NOMINALS -----------------

func (h *AdminHandler) GetNominals(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	gameID, _ := strconv.Atoi(c.Query("game_id"))
	providerID, _ := strconv.Atoi(c.Query("provider_id"))
	search := c.Query("search")
	offset := (page - 1) * limit

	nominals, total, err := h.gameService.ListNominalsAdmin(offset, limit, uint(gameID), uint(providerID), search)
	if err != nil {
		response.InternalServerError(c, "Failed to load nominals", err)
		return
	}

	response.Paginated(c, "Nominals loaded", nominals, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *AdminHandler) CreateNominal(c *gin.Context) {
	var nominal domain.Nominal
	if err := c.ShouldBindJSON(&nominal); err != nil {
		response.BadRequest(c, "Invalid nominal data", err.Error())
		return
	}

	if err := h.gameService.CreateNominal(&nominal); err != nil {
		response.InternalServerError(c, "Failed to create nominal", err)
		return
	}

	response.Created(c, "Nominal created successfully", nominal)
}

func (h *AdminHandler) UpdateNominal(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var nominal domain.Nominal
	if err := c.ShouldBindJSON(&nominal); err != nil {
		response.BadRequest(c, "Invalid nominal data", err.Error())
		return
	}

	nominal.ID = uint(id)
	if err := h.gameService.UpdateNominal(&nominal); err != nil {
		response.InternalServerError(c, "Failed to update nominal", err)
		return
	}

	response.Success(c, "Nominal updated successfully", nominal)
}

func (h *AdminHandler) DeleteNominal(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.gameService.DeleteNominal(uint(id)); err != nil {
		response.InternalServerError(c, "Failed to delete nominal", err)
		return
	}

	response.Success(c, "Nominal deleted successfully", nil)
}

// ----------------- DIGIFLAZZ SYNC & BALANCE -----------------

func (h *AdminHandler) SyncDigiflazz(c *gin.Context) {
	var req struct {
		GameID        uint    `json:"game_id" binding:"required"`
		BrandFilter   string  `json:"brand_filter"`
		MarginPercent float64 `json:"margin_percent"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid sync parameters", err.Error())
		return
	}

	count, err := h.gameService.SyncDigiflazzProducts(req.GameID, req.BrandFilter, req.MarginPercent)
	if err != nil {
		response.InternalServerError(c, "Failed to sync Digiflazz products", err)
		return
	}

	response.Success(c, "Digiflazz products synced successfully", gin.H{
		"synced_count": count,
	})
}

func (h *AdminHandler) GetDigiflazzBalance(c *gin.Context) {
	balance, err := h.digiflazzBuyer.CheckBalance()
	if err != nil {
		response.InternalServerError(c, "Failed to fetch Digiflazz balance", err)
		return
	}

	response.Success(c, "Digiflazz balance retrieved", gin.H{
		"balance": balance,
	})
}

// ----------------- TRANSACTIONS -----------------

func (h *AdminHandler) GetTransactions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	status := c.Query("status")
	search := c.Query("search")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	offset := (page - 1) * limit

	txs, total, err := h.txService.ListAdminTransactions(offset, limit, status, search, startDate, endDate)
	if err != nil {
		response.InternalServerError(c, "Failed to load transactions", err)
		return
	}

	response.Paginated(c, "Transactions loaded", txs, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *AdminHandler) ManualRetryTx(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.txService.ManualRetry(uint(id)); err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}
	response.Success(c, "Transaction retry dispatched", nil)
}

func (h *AdminHandler) ManualSuccessTx(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req struct {
		Notes string `json:"notes"`
		SN    string `json:"sn"`
	}
	_ = c.ShouldBindJSON(&req)

	notes := req.Notes
	if req.SN != "" {
		if notes != "" {
			notes = req.SN + " | " + notes
		} else {
			notes = req.SN
		}
	}

	if err := h.txService.ManualSetSuccess(uint(id), notes); err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}
	response.Success(c, "Transaction status updated to success", nil)
}

func (h *AdminHandler) ManualRefundTx(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req struct {
		Notes string `json:"notes"`
	}
	_ = c.ShouldBindJSON(&req)

	if err := h.txService.ManualRefund(uint(id), req.Notes); err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}
	response.Success(c, "Transaction refunded successfully", nil)
}

// ----------------- USERS & RESELLERS -----------------

func (h *AdminHandler) GetUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	role := c.Query("role")
	offset := (page - 1) * limit

	users, total, err := h.userRepo.List(offset, limit, role)
	if err != nil {
		response.InternalServerError(c, "Failed to load users", err)
		return
	}

	response.Paginated(c, "Users loaded", users, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *AdminHandler) CreateUser(c *gin.Context) {
	var req struct {
		Name        string      `json:"name" binding:"required"`
		Email       string      `json:"email" binding:"required,email"`
		Password    string      `json:"password" binding:"required,min=6"`
		PhoneNumber string      `json:"phone_number"`
		Role        domain.Role `json:"role" binding:"required"`
		Tier        domain.Tier `json:"tier"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid user data", err.Error())
		return
	}

	hashed, _ := crypto.HashPassword(req.Password)
	user := domain.User{
		Name:        req.Name,
		Email:       req.Email,
		Password:    hashed,
		PhoneNumber: req.PhoneNumber,
		Role:        req.Role,
		Tier:        req.Tier,
		IsActive:    true,
	}

	if err := h.userRepo.Create(&user); err != nil {
		response.InternalServerError(c, "Failed to create user", err)
		return
	}

	response.Created(c, "User created successfully", user)
}

func (h *AdminHandler) UpdateUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	user, err := h.userRepo.FindByID(uint(id))
	if err != nil || user == nil {
		response.NotFound(c, "User not found")
		return
	}

	var req struct {
		Name        string      `json:"name"`
		Role        domain.Role `json:"role"`
		Tier        domain.Tier `json:"tier"`
		IsActive    *bool       `json:"is_active"`
		BalanceAdj  float64     `json:"balance_adjustment"` // Add or subtract
		Reason      string      `json:"adjustment_reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid data", err.Error())
		return
	}

	if req.Name != "" {
		user.Name = req.Name
	}
	if req.Role != "" {
		user.Role = req.Role
	}
	if req.Tier != "" {
		user.Tier = req.Tier
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}

	_ = h.userRepo.Update(user)

	if req.BalanceAdj != 0 {
		mType := domain.MutationCredit
		adjAmount := req.BalanceAdj
		if req.BalanceAdj < 0 {
			mType = domain.MutationDebit
			adjAmount = -req.BalanceAdj
		}
		_ = h.userRepo.UpdateBalance(user.ID, adjAmount, mType, "ADMIN_ADJUSTMENT", "ADJ-MANUAL", req.Reason)
	}

	response.Success(c, "User updated successfully", user)
}

// ----------------- DEPOSITS -----------------

func (h *AdminHandler) GetDeposits(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	status := c.Query("status")
	offset := (page - 1) * limit

	deps, total, err := h.depositService.GetAdminDeposits(offset, limit, status)
	if err != nil {
		response.InternalServerError(c, "Failed to load deposits", err)
		return
	}

	response.Paginated(c, "Deposits loaded", deps, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

func (h *AdminHandler) ApproveDeposit(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	adminID, _ := c.Get("user_id")

	if err := h.depositService.ApproveDeposit(uint(id), adminID.(uint)); err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	response.Success(c, "Deposit approved successfully", nil)
}

func (h *AdminHandler) RejectDeposit(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	adminID, _ := c.Get("user_id")
	var req struct {
		Notes string `json:"notes"`
	}
	_ = c.ShouldBindJSON(&req)

	if err := h.depositService.RejectDeposit(uint(id), adminID.(uint), req.Notes); err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	response.Success(c, "Deposit rejected", nil)
}

// ----------------- PAYMENT METHODS & WEBHOOKS -----------------

func (h *AdminHandler) GetPaymentMethods(c *gin.Context) {
	methods, err := h.paymentRepo.ListAll()
	if err != nil {
		response.InternalServerError(c, "Failed to load payment methods", err)
		return
	}
	response.Success(c, "Payment methods loaded", methods)
}

func (h *AdminHandler) CreatePaymentMethod(c *gin.Context) {
	var pm domain.PaymentMethod
	if err := c.ShouldBindJSON(&pm); err != nil {
		response.BadRequest(c, "Invalid data", err.Error())
		return
	}

	if pm.Code == "" || pm.Name == "" || pm.Category == "" {
		response.BadRequest(c, "code, name, and category are required", nil)
		return
	}

	// Reject duplicate codes with a clear message instead of a raw DB
	// unique-constraint error (the table has a uniqueIndex on code).
	if existing, err := h.paymentRepo.GetByCode(pm.Code); err == nil && existing != nil && existing.ID != 0 {
		response.BadRequest(c, "A payment method with this code already exists", nil)
		return
	}

	pm.ID = 0 // ensure this always inserts a new row, never overwrites one by client-supplied id
	if err := h.paymentRepo.Create(&pm); err != nil {
		response.InternalServerError(c, "Failed to create payment method", err)
		return
	}

	response.Success(c, "Payment method created", pm)
}

func (h *AdminHandler) UpdatePaymentMethod(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var pm domain.PaymentMethod
	if err := c.ShouldBindJSON(&pm); err != nil {
		response.BadRequest(c, "Invalid data", err.Error())
		return
	}

	pm.ID = uint(id)
	if err := h.paymentRepo.Update(&pm); err != nil {
		response.InternalServerError(c, "Failed to update payment method", err)
		return
	}

	response.Success(c, "Payment method updated", pm)
}

func (h *AdminHandler) DeletePaymentMethod(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.paymentRepo.Delete(uint(id)); err != nil {
		response.InternalServerError(c, "Failed to delete payment method", err)
		return
	}
	response.Success(c, "Payment method deleted", nil)
}

// SyncTripayPaymentMethods pulls the full channel list (with fees) from
// Tripay's own API and upserts them into payment_methods, so channels like
// DANA, QRIS, or Indomaret don't need to be typed in by hand and stay
// current if Tripay changes a fee.
func (h *AdminHandler) SyncTripayPaymentMethods(c *gin.Context) {
	if h.tripayChannelService == nil {
		response.InternalServerError(c, "Tripay sync is not configured", nil)
		return
	}

	result, err := h.tripayChannelService.SyncPaymentMethods()
	if err != nil {
		response.InternalServerError(c, "Failed to sync payment methods from Tripay", err)
		return
	}

	response.Success(c, "Payment methods synced from Tripay", result)
}

func (h *AdminHandler) GetWebhookLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	provider := c.Query("provider")
	offset := (page - 1) * limit

	logs, total, err := h.providerRepo.ListWebhookLogs(offset, limit, provider)
	if err != nil {
		response.InternalServerError(c, "Failed to load webhook logs", err)
		return
	}

	response.Paginated(c, "Webhook logs loaded", logs, gin.H{
		"page":  page,
		"limit": limit,
		"total": total,
	})
}

// ====================== BANNERS ======================

func (h *AdminHandler) GetBanners(c *gin.Context) {
	banners, err := h.bannerRepo.List(false)
	if err != nil {
		response.InternalServerError(c, "Failed to load banners", err)
		return
	}
	response.Success(c, "Banners loaded", banners)
}

func (h *AdminHandler) CreateBanner(c *gin.Context) {
	var b domain.Banner
	if err := c.ShouldBindJSON(&b); err != nil {
		response.BadRequest(c, "Invalid banner data", err.Error())
		return
	}
	if err := h.bannerRepo.Create(&b); err != nil {
		response.InternalServerError(c, "Failed to create banner", err)
		return
	}
	response.Created(c, "Banner created", b)
}

func (h *AdminHandler) UpdateBanner(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var b domain.Banner
	if err := c.ShouldBindJSON(&b); err != nil {
		response.BadRequest(c, "Invalid banner data", err.Error())
		return
	}
	b.ID = uint(id)
	if err := h.bannerRepo.Update(&b); err != nil {
		response.InternalServerError(c, "Failed to update banner", err)
		return
	}
	response.Success(c, "Banner updated", b)
}

func (h *AdminHandler) DeleteBanner(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.bannerRepo.Delete(uint(id)); err != nil {
		response.InternalServerError(c, "Failed to delete banner", err)
		return
	}
	response.Success(c, "Banner deleted", nil)
}

// ====================== PUBLIC BANNERS (no auth) ======================

func (h *AdminHandler) GetPublicBanners(c *gin.Context) {
	banners, err := h.bannerRepo.List(true) // activeOnly = true
	if err != nil {
		response.InternalServerError(c, "Failed to load banners", err)
		return
	}
	response.Success(c, "Banners loaded", banners)
}

// ====================== ARTICLES ======================

func (h *AdminHandler) GetArticles(c *gin.Context) {
	category := c.Query("category")
	articles, err := h.articleRepo.List(false, category)
	if err != nil {
		response.InternalServerError(c, "Failed to load articles", err)
		return
	}
	response.Success(c, "Articles loaded", articles)
}

func (h *AdminHandler) CreateArticle(c *gin.Context) {
	var a domain.Article
	if err := c.ShouldBindJSON(&a); err != nil {
		response.BadRequest(c, "Invalid article data", err.Error())
		return
	}
	if err := h.articleRepo.Create(&a); err != nil {
		response.InternalServerError(c, "Failed to create article", err)
		return
	}
	response.Created(c, "Article created", a)
}

func (h *AdminHandler) UpdateArticle(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var a domain.Article
	if err := c.ShouldBindJSON(&a); err != nil {
		response.BadRequest(c, "Invalid article data", err.Error())
		return
	}
	a.ID = uint(id)
	if err := h.articleRepo.Update(&a); err != nil {
		response.InternalServerError(c, "Failed to update article", err)
		return
	}
	response.Success(c, "Article updated", a)
}

func (h *AdminHandler) DeleteArticle(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.articleRepo.Delete(uint(id)); err != nil {
		response.InternalServerError(c, "Failed to delete article", err)
		return
	}
	response.Success(c, "Article deleted", nil)
}

// ====================== PUBLIC ARTICLES (no auth) ======================

func (h *AdminHandler) GetPublicArticles(c *gin.Context) {
	category := c.Query("category")
	articles, err := h.articleRepo.List(true, category) // publishedOnly = true
	if err != nil {
		response.InternalServerError(c, "Failed to load articles", err)
		return
	}
	response.Success(c, "Articles loaded", articles)
}

// ====================== PROVIDERS & BATCH SWITCH ======================

func (h *AdminHandler) GetProviders(c *gin.Context) {
	providers, err := h.providerRepo.List()
	if err != nil {
		response.InternalServerError(c, "Failed to load providers", err)
		return
	}
	response.Success(c, "Providers loaded", providers)
}

type BatchSwitchProviderRequest struct {
	NominalIDs []uint `json:"nominal_ids"`
	GameID     uint   `json:"game_id"`
	ProviderID uint   `json:"provider_id" binding:"required"`
}

func (h *AdminHandler) BatchSwitchProvider(c *gin.Context) {
	var req BatchSwitchProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload", err.Error())
		return
	}

	if req.ProviderID == 0 {
		response.BadRequest(c, "provider_id is required", nil)
		return
	}

	if len(req.NominalIDs) > 0 {
		result, err := h.gameService.BatchSwitchProvider(req.NominalIDs, req.ProviderID)
		if err != nil {
			response.InternalServerError(c, "Gagal mengalihkan provider untuk nominal terpilih", err)
			return
		}
		response.Success(c, result.Message, result)
		return
	}

	if req.GameID > 0 {
		result, err := h.gameService.SwitchProviderByGame(req.GameID, req.ProviderID)
		if err != nil {
			response.InternalServerError(c, "Gagal mengalihkan provider untuk produk game ini", err)
			return
		}
		response.Success(c, result.Message, result)
		return
	}

	response.BadRequest(c, "Either nominal_ids or game_id must be provided", nil)
}