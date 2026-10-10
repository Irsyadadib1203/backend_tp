package service

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/pkg/utils"
	"topup-backend/internal/pkg/worker"
	"topup-backend/internal/repository"
)

// Payload sign per endpoint: sign = md5(apikey + payload)
const (
	H2HSignBalance   = "balance"
	H2HSignPriceList = "pricelist"
	H2HSignCategory  = "category"
	H2HSignProduct   = "product"
	// Untuk transaksi & cek status, payload-nya adalah ref_id.
)

var (
	ErrH2HInvalidAPIKey   = errors.New("invalid API key")
	ErrH2HAccountDisabled = errors.New("account or API key is disabled")
	ErrH2HInvalidSign     = errors.New("invalid signature")
	ErrH2HBrandNotFound   = errors.New("brand not found")
)

// IsH2HAuthError true jika error berasal dari proses autentikasi partner.
func IsH2HAuthError(err error) bool {
	return errors.Is(err, ErrH2HInvalidAPIKey) ||
		errors.Is(err, ErrH2HAccountDisabled) ||
		errors.Is(err, ErrH2HInvalidSign)
}

// ---------------------------------------------------------------------------
// Request / Response
// ---------------------------------------------------------------------------

type H2HCategoryRequest struct {
	APIKey string `json:"apikey" binding:"required"`
	Sign   string `json:"sign" binding:"required"`
}

type H2HPriceListRequest struct {
	APIKey string `json:"apikey" binding:"required"`
	Sign   string `json:"sign" binding:"required"`
}

type H2HProductRequest struct {
	APIKey string `json:"apikey" binding:"required"`
	Sign   string `json:"sign" binding:"required"`
	Brand  string `json:"brand" binding:"required"`
}

type H2HTransactionRequest struct {
	APIKey       string `json:"apikey" binding:"required"`
	SKUCode     string `json:"sku_code" binding:"required"`
	UserID       string `json:"user_id" binding:"required"`
	ServerID     string `json:"server_id,omitempty"` // optional
	RefID        string `json:"ref_id" binding:"required"`
	Sign         string `json:"sign" binding:"required"`
	Testing      bool   `json:"testing"`
	CallbackURL  string `json:"callback_url,omitempty"`
}

type H2HCheckStatusRequest struct {
	APIKey string `json:"apikey" binding:"required"`
	RefID  string `json:"ref_id" binding:"required"`
	Sign   string `json:"sign" binding:"required"`
}

type H2HCheckBalanceRequest struct {
	APIKey string `json:"apikey" binding:"required"`
	Sign   string `json:"sign" binding:"required"`
}

type H2HResponseData struct {
	RefID        string  `json:"ref_id"`
	InvoiceNumber string `json:"invoice_number"`
	UserID       string  `json:"user_id"`
	ServerID     string  `json:"server_id,omitempty"`
	SKUCode      string  `json:"sku_code"`
	Message      string  `json:"message"`
	Status       string  `json:"status"` // "Sukses", "Pending", "Gagal"
	RC           string  `json:"rc"`     // "00", "03", "40", etc.
	SN           string  `json:"sn"`
	Price        float64 `json:"price"`
	Sign         string  `json:"sign,omitempty"`
}

type H2HProduct struct {
	ProductName         string  `json:"product_name"`
	Category            string  `json:"category"`
	Brand               string  `json:"brand"`
	Price               float64 `json:"price"`
	SKUCode             string  `json:"sku_code"`
	BuyerProductStatus  bool    `json:"buyer_product_status"`
	SellerProductStatus bool    `json:"seller_product_status"`
}

type H2HBrand struct {
	Brand        string `json:"brand"`
	Category     string `json:"category"`
	TotalProduct int    `json:"total_product"`
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

type H2HService interface {
	GetCategories(req *H2HCategoryRequest) ([]H2HBrand, error)
	GetPriceList(req *H2HPriceListRequest) ([]H2HProduct, error)
	GetProductsByBrand(req *H2HProductRequest) ([]H2HProduct, error)
	ProcessTransaction(req *H2HTransactionRequest, clientIP string) (*H2HResponseData, error)
	CheckStatus(req *H2HCheckStatusRequest) (*H2HResponseData, error)
	CheckBalance(req *H2HCheckBalanceRequest) (float64, error)
	AuthenticatePartner(apiKey, sign, signPayload string) (*domain.User, *domain.APIKey, error)
}

type h2hService struct {
	userRepo       repository.UserRepository
	nominalRepo    repository.NominalRepository
	txRepo         repository.TransactionRepository
	digiflazzBuyer DigiflazzBuyerService
	webhookService WebhookService
}

func NewH2HService(
	userRepo repository.UserRepository,
	nominalRepo repository.NominalRepository,
	txRepo repository.TransactionRepository,
	digiflazzBuyer DigiflazzBuyerService,
	webhookService WebhookService,
) H2HService {
	return &h2hService{
		userRepo:       userRepo,
		nominalRepo:    nominalRepo,
		txRepo:         txRepo,
		digiflazzBuyer: digiflazzBuyer,
		webhookService: webhookService,
	}
}

// AuthenticatePartner memverifikasi apikey dan sign = md5(apikey + signPayload).
func (s *h2hService) AuthenticatePartner(apiKeyString, sign, signPayload string) (*domain.User, *domain.APIKey, error) {
	user, apiKey, err := s.userRepo.FindByAPIKey(strings.TrimSpace(apiKeyString))
	if err != nil || apiKey == nil || user == nil {
		return nil, nil, ErrH2HInvalidAPIKey
	}

	if !apiKey.IsActive || !user.IsActive {
		return nil, nil, ErrH2HAccountDisabled
	}

	expected := strings.ToLower(crypto.MD5Hash(apiKey.Key + signPayload))
	given := strings.ToLower(strings.TrimSpace(sign))
	if subtle.ConstantTimeCompare([]byte(given), []byte(expected)) != 1 {
		return nil, nil, ErrH2HInvalidSign
	}

	now := time.Now()
	apiKey.LastUsedAt = &now
	_ = s.userRepo.CreateAPIKey(apiKey)

	return user, apiKey, nil
}

func partnerPrice(user *domain.User, reseller, vip float64) float64 {
	if user.Tier == domain.TierVIP {
		return vip
	}
	return reseller
}

// buildProducts mengubah nominal menjadi produk H2H dengan harga sesuai tier partner.
func (s *h2hService) buildProducts(user *domain.User) ([]H2HProduct, error) {
	nominals, err := s.nominalRepo.ListForSellerH2H()
	if err != nil {
		return nil, err
	}

	products := make([]H2HProduct, 0, len(nominals))
	for _, nom := range nominals {
		sku := nom.SellerProductCode
		if sku == "" {
			sku = nom.ProviderProductCode
		}

		brand := "GAME"
		category := "Games"
		if nom.Game != nil {
			brand = nom.Game.Name
			category = string(nom.Game.Category)
		}

		products = append(products, H2HProduct{
			ProductName:         nom.Name,
			Category:            category,
			Brand:               brand,
			Price:               partnerPrice(user, nom.PriceReseller, nom.PriceVIP),
			SKUCode:             sku,
		})
	}
	return products, nil
}

func (s *h2hService) GetCategories(req *H2HCategoryRequest) ([]H2HBrand, error) {
	user, _, err := s.AuthenticatePartner(req.APIKey, req.Sign, H2HSignCategory)
	if err != nil {
		return nil, err
	}

	products, err := s.buildProducts(user)
	if err != nil {
		return nil, err
	}

	grouped := make(map[string]*H2HBrand)
	for _, p := range products {
		key := strings.ToLower(p.Brand)
		if b, ok := grouped[key]; ok {
			b.TotalProduct++
			continue
		}
		grouped[key] = &H2HBrand{Brand: p.Brand, Category: p.Category, TotalProduct: 1}
	}

	result := make([]H2HBrand, 0, len(grouped))
	for _, b := range grouped {
		result = append(result, *b)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Brand) < strings.ToLower(result[j].Brand)
	})
	return result, nil
}

func (s *h2hService) GetPriceList(req *H2HPriceListRequest) ([]H2HProduct, error) {
	user, _, err := s.AuthenticatePartner(req.APIKey, req.Sign, H2HSignPriceList)
	if err != nil {
		return nil, err
	}
	return s.buildProducts(user)
}

func (s *h2hService) GetProductsByBrand(req *H2HProductRequest) ([]H2HProduct, error) {
	user, _, err := s.AuthenticatePartner(req.APIKey, req.Sign, H2HSignProduct)
	if err != nil {
		return nil, err
	}

	products, err := s.buildProducts(user)
	if err != nil {
		return nil, err
	}

	brand := strings.TrimSpace(req.Brand)
	result := make([]H2HProduct, 0)
	for _, p := range products {
		if strings.EqualFold(p.Brand, brand) {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil, ErrH2HBrandNotFound
	}
	return result, nil
}

func (s *h2hService) ProcessTransaction(req *H2HTransactionRequest, clientIP string) (*H2HResponseData, error) {
	userID := strings.TrimSpace(req.UserID)
	serverID := strings.TrimSpace(req.ServerID)

	fail := func(msg, rc string, price float64) *H2HResponseData {
		return &H2HResponseData{
			RefID:        req.RefID,
			UserID:       userID,
			ServerID:     serverID,
			SKUCode:      req.SKUCode,
			Message:      msg,
			Status:       "Gagal",
			RC:           rc,
			Price:        price,
		}
	}

	// 1. Authenticate Partner: sign = md5(apikey + ref_id)
	user, apiKey, err := s.AuthenticatePartner(req.APIKey, req.Sign, req.RefID)
	if err != nil {
		return fail(err.Error(), "40", 0), err
	}

	if userID == "" {
		return fail("user_id wajib diisi", "40", 0), errors.New("user_id is required")
	}
		callbackURL := strings.TrimSpace(req.CallbackURL)
	if callbackURL != "" {
		if err := ValidatePartnerWebhookURL(callbackURL); err != nil {
			return fail("callback_url tidak valid: "+err.Error(), "40", 0), err
		}
	}

	// 2. Idempotency per partner + ref_id
	idemKey := fmt.Sprintf("h2h_%d_%s", user.ID, req.RefID)
	existingTx, _ := s.txRepo.FindByIdempotencyKey(idemKey)
	if existingTx != nil {
		return &H2HResponseData{
			RefID:        req.RefID,
			InvoiceNumber: existingTx.InvoiceNumber,
			UserID:       existingTx.CustomerID,
			ServerID:     existingTx.ServerID,
			SKUCode:      req.SKUCode,
			Message:      existingTx.ProviderMessage,
			Status:       statusToDigiflazz(existingTx.Status),
			RC:           statusToRC(existingTx.Status),
			SN:           existingTx.PaymentReference,
			Price:        existingTx.SellingPrice,
		}, nil
	}

	// 3. Find Nominal / Product
	nominal, err := s.nominalRepo.FindBySellerCode(req.SKUCode)
	if err != nil || nominal == nil || !nominal.IsActive {
		return fail("Produk tidak ditemukan atau sedang gangguan", "07", 0), errors.New("product not available")
	}

	price := partnerPrice(user, nominal.PriceReseller, nominal.PriceVIP)

	// Margin guard: jangan bocorkan detail provider
	if nominal.BasePrice > price {
		return fail("Produk sedang dalam pemeliharaan", "40", price), errors.New("product under maintenance")
	}

	// 4. Cek saldo
	if user.Balance < price {
		return fail("Saldo deposit Anda tidak mencukupi", "17", price), errors.New("insufficient balance")
	}

	// 5. Invoice & potong saldo
	invoiceNumber := utils.GenerateInvoiceNumber()
	refIDProvider := utils.GenerateRefID()

	err = s.userRepo.UpdateBalance(user.ID, price, domain.MutationDebit, "TRANSACTION_H2H", invoiceNumber,
		fmt.Sprintf("Top up %s (%s)", nominal.Name, formatTarget(userID, serverID)))
	if err != nil {
		return fail("Gagal memotong saldo: "+err.Error(), "17", price), err
	}

	tx := &domain.Transaction{
		InvoiceNumber:   invoiceNumber,
		IdempotencyKey:  idemKey,
		Source:          domain.SourceH2H,
		UserID:          &user.ID,
		CustomerID:      userID,
		ServerID:        serverID,
		GameID:          nominal.GameID,
		NominalID:       nominal.ID,
		ProviderID:      nominal.ProviderID,
		BasePrice:       nominal.BasePrice,
		SellingPrice:    price,
		AdminFee:        0,
		TotalAmount:     price,
		Profit:          price - nominal.BasePrice,
		Status:          domain.StatusProcessing,
		PaymentMethod:   "SALDO_H2H",
		RefID:           refIDProvider,
		ProviderOrderID: req.RefID,
		PartnerCallbackURL: callbackURL,
	}

	if err := s.txRepo.Create(tx); err != nil {
		_ = s.userRepo.UpdateBalance(user.ID, price, domain.MutationCredit, "REFUND", invoiceNumber, "Refund gagal create order")
		return fail("Gagal membuat transaksi", "40", price), err
	}

	// 6. Forward ke provider
	providerStatus := "Pending"
	providerRC := "03"
	providerMsg := "Transaksi sedang diproses"
	snNumber := ""

	// Digiflazz tetap memakai satu field customer_no = user_id + server_id (server_id opsional).
	customerNo := userID + serverID
	digiResp, err := s.digiflazzBuyer.CreateTransaction(refIDProvider, nominal.ProviderProductCode, customerNo, req.Testing)
	if err == nil && digiResp != nil {
		providerStatus = digiResp.Data.Status
		providerRC = digiResp.Data.RC
		providerMsg = digiResp.Data.Message
		snNumber = digiResp.Data.SN

		tx.ProviderStatus = providerStatus
		tx.ProviderMessage = providerMsg
		tx.PaymentReference = snNumber

		switch providerStatus {
		case "Sukses":
			tx.Status = domain.StatusSuccess
			now := time.Now()
			tx.CompletedAt = &now
		case "Gagal":
			tx.Status = domain.StatusFailed
			now := time.Now()
			tx.CompletedAt = &now
			hasRefunded, _ := s.userRepo.HasMutation("REFUND", invoiceNumber)
			if !hasRefunded {
				_ = s.userRepo.UpdateBalance(user.ID, price, domain.MutationCredit, "REFUND", invoiceNumber, "Pengembalian dana transaksi gagal")
			}
		default:
			tx.Status = domain.StatusProcessing
		}
		_ = s.txRepo.Update(tx)
	} else {
		tx.Status = domain.StatusProcessing
		tx.ProviderMessage = "Sedang dalam antrean provider"
		_ = s.txRepo.Update(tx)
	}

	// 7. Webhook ke partner
	targetWebhook := callbackURL
	if targetWebhook == "" && apiKey.WebhookURL != "" {
		targetWebhook = apiKey.WebhookURL
	}
	if targetWebhook != "" && isFinalTransactionStatus(tx.Status) {
		h2hData := &H2HResponseData{
			RefID:        req.RefID,
			InvoiceNumber: invoiceNumber,
			UserID:       userID,
			ServerID:     serverID,
			SKUCode:      req.SKUCode,
			Message:      providerMsg,
			Status:       providerStatus,
			RC:           providerRC,
			SN:           snNumber,
			Price:        price,
		}
		// Tanpa secret: webhook ditandatangani memakai apikey partner.
		webhookKey := apiKey.Key
		worker.GlobalPool.Submit(func() {
			s.webhookService.DispatchH2HCallback(targetWebhook, webhookKey, h2hData)
		})
	}

		return &H2HResponseData{
		RefID:         req.RefID,
		InvoiceNumber: invoiceNumber,
		UserID:        userID,
		ServerID:      serverID,
		SKUCode:       req.SKUCode,
		Message:       providerMsg,
		Status:        providerStatus,
		RC:            providerRC,
		SN:            snNumber,
		Price:         price,
		Sign:          crypto.MD5Hash(apiKey.Key + req.RefID),
	}, nil
}

func (s *h2hService) CheckStatus(req *H2HCheckStatusRequest) (*H2HResponseData, error) {
	user, apiKey, err := s.AuthenticatePartner(req.APIKey, req.Sign, req.RefID)
	if err != nil {
		return nil, err
	}

	tx, err := s.txRepo.FindByRefIDAndUserID(
        req.RefID,
        user.ID,
    )
    if err != nil || tx == nil {
        return nil, errors.New("transaction not found")
    }

	skuCode := ""
	if nominal, _ := s.nominalRepo.FindByID(tx.NominalID); nominal != nil {
		skuCode = nominal.SellerProductCode
	}

	return &H2HResponseData{
		RefID:        req.RefID,
		InvoiceNumber: tx.InvoiceNumber,
		UserID:       tx.CustomerID,
		ServerID:     tx.ServerID,
		SKUCode:      skuCode,
		Message:      tx.ProviderMessage,
		Status:       statusToDigiflazz(tx.Status),
		RC:           statusToRC(tx.Status),
		SN:           tx.PaymentReference,
		Price:        tx.SellingPrice,
		Sign:         crypto.MD5Hash(apiKey.Key + req.RefID),
	}, nil
}

func (s *h2hService) CheckBalance(req *H2HCheckBalanceRequest) (float64, error) {
	user, _, err := s.AuthenticatePartner(req.APIKey, req.Sign, H2HSignBalance)
	if err != nil {
		return 0, err
	}
	return user.Balance, nil
}

func formatTarget(userID, serverID string) string {
	if serverID == "" {
		return userID
	}
	return userID + "(" + serverID + ")"
}

func statusToDigiflazz(st domain.TransactionStatus) string {
	switch st {
	case domain.StatusSuccess:
		return "Sukses"
	case domain.StatusFailed, domain.StatusRefunded:
		return "Gagal"
	default:
		return "Pending"
	}
}

func statusToRC(st domain.TransactionStatus) string {
	switch st {
	case domain.StatusSuccess:
		return "00"
	case domain.StatusFailed:
		return "40"
	default:
		return "03"
	}
}