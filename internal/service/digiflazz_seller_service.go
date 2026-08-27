package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/pkg/utils"
	"topup-backend/internal/pkg/worker"
	"topup-backend/internal/repository"
)

type SellerPriceListRequest struct {
	Cmd      string `json:"cmd"`
	Username string `json:"username"`
	Sign     string `json:"sign"`
	Code     string `json:"code,omitempty"`
}

type SellerTransactionRequest struct {
	Username     string `json:"username" binding:"required"`
	BuyerSkuCode string `json:"buyer_sku_code" binding:"required"`
	CustomerNo   string `json:"customer_no" binding:"required"`
	RefID        string `json:"ref_id" binding:"required"`
	Sign         string `json:"sign" binding:"required"`
	Testing      bool   `json:"testing"`
	CallbackURL  string `json:"callback_url,omitempty"`
}

type SellerCheckBalanceRequest struct {
	Cmd      string `json:"cmd"`
	Username string `json:"username" binding:"required"`
	Sign     string `json:"sign" binding:"required"`
}

type SellerH2HResponseData struct {
	RefID        string  `json:"ref_id"`
	CustomerNo   string  `json:"customer_no"`
	BuyerSkuCode string  `json:"buyer_sku_code"`
	Message      string  `json:"message"`
	Status       string  `json:"status"` // "Sukses", "Pending", "Gagal"
	RC           string  `json:"rc"`     // "00", "03", "40", etc.
	SN           string  `json:"sn"`
	Price        float64 `json:"price"`
	Sign         string  `json:"sign,omitempty"`
}

type DigiflazzSellerService interface {
	GetPriceList(req *SellerPriceListRequest) ([]map[string]interface{}, error)
	ProcessH2HTransaction(req *SellerTransactionRequest, clientIP string) (*SellerH2HResponseData, error)
	CheckH2HStatus(username, refID, sign string) (*SellerH2HResponseData, error)
	CheckH2HBalance(req *SellerCheckBalanceRequest) (float64, error)
	AuthenticatePartner(apiKey, sign, signPayload string) (*domain.User, *domain.APIKey, error)
}

type digiflazzSellerService struct {
	userRepo        repository.UserRepository
	nominalRepo     repository.NominalRepository
	txRepo          repository.TransactionRepository
	digiflazzBuyer  DigiflazzBuyerService
	webhookService  WebhookService
}

func NewDigiflazzSellerService(
	userRepo repository.UserRepository,
	nominalRepo repository.NominalRepository,
	txRepo repository.TransactionRepository,
	digiflazzBuyer DigiflazzBuyerService,
	webhookService WebhookService,
) DigiflazzSellerService {
	return &digiflazzSellerService{
		userRepo:       userRepo,
		nominalRepo:    nominalRepo,
		txRepo:         txRepo,
		digiflazzBuyer: digiflazzBuyer,
		webhookService: webhookService,
	}
}

func (s *digiflazzSellerService) AuthenticatePartner(apiKeyString, sign, signPayload string) (*domain.User, *domain.APIKey, error) {
	user, apiKey, err := s.userRepo.FindByAPIKey(apiKeyString)
	if err != nil || apiKey == nil || user == nil {
		return nil, nil, errors.New("invalid API key / username")
	}

	if !apiKey.IsActive || !user.IsActive {
		return nil, nil, errors.New("account or API key is disabled")
	}

	// Verify signature: md5(username + secret + signPayload)
	expectedSign := crypto.MD5Hash(apiKey.Key + apiKey.Secret + signPayload)
	if strings.ToLower(sign) != strings.ToLower(expectedSign) {
		return nil, nil, errors.New("invalid signature")
	}

	// Update last used timestamp
	now := time.Now()
	apiKey.LastUsedAt = &now
	_ = s.userRepo.CreateAPIKey(apiKey)

	return user, apiKey, nil
}

func (s *digiflazzSellerService) GetPriceList(req *SellerPriceListRequest) ([]map[string]interface{}, error) {
	nominals, err := s.nominalRepo.ListForSellerH2H()
	if err != nil {
		return nil, err
	}

	var results []map[string]interface{}
	for _, nom := range nominals {
		skuCode := nom.SellerProductCode
		if skuCode == "" {
			skuCode = nom.ProviderProductCode
		}

		brand := "GAME"
		category := "Games"
		if nom.Game != nil {
			brand = nom.Game.Name
			category = string(nom.Game.Category)
		}

		results = append(results, map[string]interface{}{
			"product_name":          nom.Name,
			"category":              category,
			"brand":                 brand,
			"type":                  "Umum",
			"seller_name":           "TopUp Engine",
			"price":                 nom.PriceReseller,
			"buyer_sku_code":        skuCode,
			"buyer_product_status":  nom.IsActive,
			"seller_product_status": nom.IsActive,
			"unlimited_stock":       true,
			"stock":                 9999,
			"multi":                 true,
			"start_cut_off":         "00:00",
			"end_cut_off":           "23:59",
			"desc":                  nom.Description,
		})
	}

	return results, nil
}

func (s *digiflazzSellerService) ProcessH2HTransaction(req *SellerTransactionRequest, clientIP string) (*SellerH2HResponseData, error) {
	// 1. Authenticate Partner
	user, apiKey, err := s.AuthenticatePartner(req.Username, req.Sign, req.RefID)
	if err != nil {
		return &SellerH2HResponseData{
			RefID:        req.RefID,
			CustomerNo:   req.CustomerNo,
			BuyerSkuCode: req.BuyerSkuCode,
			Message:      err.Error(),
			Status:       "Gagal",
			RC:           "40",
			Price:        0,
		}, err
	}

	// 2. Check Idempotency / Existing RefID from this partner
	existingTx, _ := s.txRepo.FindByIdempotencyKey(fmt.Sprintf("h2h_%d_%s", user.ID, req.RefID))
	if existingTx != nil {
		return &SellerH2HResponseData{
			RefID:        req.RefID,
			CustomerNo:   existingTx.CustomerID,
			BuyerSkuCode: req.BuyerSkuCode,
			Message:      existingTx.ProviderMessage,
			Status:       statusToDigiflazz(existingTx.Status),
			RC:           statusToRC(existingTx.Status),
			SN:           existingTx.PaymentReference,
			Price:        existingTx.SellingPrice,
		}, nil
	}

	// 3. Find Nominal / Product
	nominal, err := s.nominalRepo.FindBySellerCode(req.BuyerSkuCode)
	if err != nil || nominal == nil || !nominal.IsActive {
		return &SellerH2HResponseData{
			RefID:        req.RefID,
			CustomerNo:   req.CustomerNo,
			BuyerSkuCode: req.BuyerSkuCode,
			Message:      "Produk tidak ditemukan atau sedang gangguan",
			Status:       "Gagal",
			RC:           "07",
			Price:        0,
		}, errors.New("product not available")
	}

	price := nominal.PriceReseller
	if user.Tier == domain.TierVIP {
		price = nominal.PriceVIP
	}

	// White-Label Margin Guard: If cost exceeds selling price, reject gracefully without revealing provider details
	if nominal.BasePrice > price {
		return &SellerH2HResponseData{
			RefID:        req.RefID,
			CustomerNo:   req.CustomerNo,
			BuyerSkuCode: req.BuyerSkuCode,
			Message:      "Produk sedang dalam pemeliharaan",
			Status:       "Gagal",
			RC:           "40",
			Price:        price,
		}, errors.New("product under maintenance")
	}

	// 4. Check Partner Deposit Balance
	if user.Balance < price {
		return &SellerH2HResponseData{
			RefID:        req.RefID,
			CustomerNo:   req.CustomerNo,
			BuyerSkuCode: req.BuyerSkuCode,
			Message:      "Saldo deposit Anda tidak mencukupi",
			Status:       "Gagal",
			RC:           "17",
			Price:        price,
		}, errors.New("insufficient balance")
	}

	// 5. Generate Internal Invoice & Deduct Balance
	invoiceNumber := utils.GenerateInvoiceNumber()
	refIDProvider := utils.GenerateRefID()

	err = s.userRepo.UpdateBalance(user.ID, price, domain.MutationDebit, "TRANSACTION_H2H", invoiceNumber, fmt.Sprintf("Top up %s (%s)", nominal.Name, req.CustomerNo))
	if err != nil {
		return &SellerH2HResponseData{
			RefID:        req.RefID,
			CustomerNo:   req.CustomerNo,
			BuyerSkuCode: req.BuyerSkuCode,
			Message:      "Gagal memotong saldo: " + err.Error(),
			Status:       "Gagal",
			RC:           "17",
			Price:        price,
		}, err
	}

	// Parse Customer ID & Server ID if formatted like "12345678(1234)"
	customerID := req.CustomerNo
	serverID := ""
	if strings.Contains(customerID, "(") && strings.Contains(customerID, ")") {
		parts := strings.Split(customerID, "(")
		customerID = parts[0]
		serverID = strings.Trim(parts[1], ")")
	}

	tx := &domain.Transaction{
		InvoiceNumber:   invoiceNumber,
		IdempotencyKey:  fmt.Sprintf("h2h_%d_%s", user.ID, req.RefID),
		Source:          domain.SourceH2H,
		UserID:          &user.ID,
		CustomerID:      customerID,
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
	}

	if err := s.txRepo.Create(tx); err != nil {
		// Refund on error
		_ = s.userRepo.UpdateBalance(user.ID, price, domain.MutationCredit, "REFUND", invoiceNumber, "Refund gagal create order")
		return nil, err
	}

	// 6. Forward to Digiflazz Buyer Provider
	var providerStatus = "Pending"
	var providerRC = "03"
	var providerMsg = "Transaksi sedang diproses"
	var snNumber = ""

	digiResp, err := s.digiflazzBuyer.CreateTransaction(refIDProvider, nominal.ProviderProductCode, customerID, req.Testing)
	if err == nil && digiResp != nil {
		providerStatus = digiResp.Data.Status
		providerRC = digiResp.Data.RC
		providerMsg = digiResp.Data.Message
		snNumber = digiResp.Data.SN

		tx.ProviderStatus = providerStatus
		tx.ProviderMessage = providerMsg
		tx.PaymentReference = snNumber

		if providerStatus == "Sukses" {
			tx.Status = domain.StatusSuccess
			now := time.Now()
			tx.CompletedAt = &now
		} else if providerStatus == "Gagal" {
			tx.Status = domain.StatusFailed
			now := time.Now()
			tx.CompletedAt = &now
			// Auto refund to partner
			_ = s.userRepo.UpdateBalance(user.ID, price, domain.MutationCredit, "REFUND", invoiceNumber, "Pengembalian dana transaksi gagal")
		} else {
			tx.Status = domain.StatusProcessing
		}
		_ = s.txRepo.Update(tx)
	} else {
		tx.Status = domain.StatusProcessing
		tx.ProviderMessage = "Sedang dalam antrean provider"
		_ = s.txRepo.Update(tx)
	}

	// 7. Dispatch Webhook to Partner if callback URL configured
	targetWebhook := req.CallbackURL
	if targetWebhook == "" && apiKey.WebhookURL != "" {
		targetWebhook = apiKey.WebhookURL
	}
	if targetWebhook != "" {
		h2hData := &SellerH2HResponseData{
			RefID:        req.RefID,
			CustomerNo:   req.CustomerNo,
			BuyerSkuCode: req.BuyerSkuCode,
			Message:      providerMsg,
			Status:       providerStatus,
			RC:           providerRC,
			SN:           snNumber,
			Price:        price,
		}
		secret := apiKey.Secret
		worker.GlobalPool.Submit(func() {
			s.webhookService.DispatchH2HCallback(targetWebhook, secret, h2hData)
		})
	}

	return &SellerH2HResponseData{
		RefID:        req.RefID,
		CustomerNo:   req.CustomerNo,
		BuyerSkuCode: req.BuyerSkuCode,
		Message:      providerMsg,
		Status:       providerStatus,
		RC:           providerRC,
		SN:           snNumber,
		Price:        price,
		Sign:         crypto.MD5Hash(apiKey.Key + apiKey.Secret + req.RefID),
	}, nil
}

func (s *digiflazzSellerService) CheckH2HStatus(username, refID, sign string) (*SellerH2HResponseData, error) {
	user, apiKey, err := s.AuthenticatePartner(username, sign, refID)
	if err != nil {
		return nil, err
	}

	tx, err := s.txRepo.FindByIdempotencyKey(fmt.Sprintf("h2h_%d_%s", user.ID, refID))
	if err != nil || tx == nil {
		return nil, errors.New("transaction not found")
	}

	nominal, _ := s.nominalRepo.FindByID(tx.NominalID)
	skuCode := ""
	if nominal != nil {
		skuCode = nominal.SellerProductCode
	}

	return &SellerH2HResponseData{
		RefID:        refID,
		CustomerNo:   tx.CustomerID,
		BuyerSkuCode: skuCode,
		Message:      tx.ProviderMessage,
		Status:       statusToDigiflazz(tx.Status),
		RC:           statusToRC(tx.Status),
		SN:           tx.PaymentReference,
		Price:        tx.SellingPrice,
		Sign:         crypto.MD5Hash(apiKey.Key + apiKey.Secret + refID),
	}, nil
}

func (s *digiflazzSellerService) CheckH2HBalance(req *SellerCheckBalanceRequest) (float64, error) {
	user, _, err := s.AuthenticatePartner(req.Username, req.Sign, "depo")
	if err != nil {
		return 0, err
	}
	return user.Balance, nil
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
