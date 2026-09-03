package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/sse"
	"topup-backend/internal/pkg/utils"
	"topup-backend/internal/pkg/worker"
	"topup-backend/internal/repository"
)

type CreateOrderRequest struct {
	GameID        uint    `json:"game_id" binding:"required"`
	NominalID     uint    `json:"nominal_id" binding:"required"`
	CustomerID    string  `json:"customer_id" binding:"required"`
	ServerID      string  `json:"server_id"`
	CustomerPhone string  `json:"customer_phone"`
	CustomerEmail string  `json:"customer_email"`
	Nickname      string  `json:"nickname"`
	PaymentMethod string  `json:"payment_method" binding:"required"`
	UserID        *uint   `json:"user_id"`
}

type TransactionService interface {
	CreateOrder(req *CreateOrderRequest) (*domain.Transaction, error)
	GetByInvoice(invoice string) (*domain.Transaction, error)
	ListRecent(limit int) ([]domain.Transaction, error)
	ListUserTransactions(userID uint, offset, limit int) ([]domain.Transaction, int64, error)
	ListAdminTransactions(offset, limit int, status, search, startDate, endDate string) ([]domain.Transaction, int64, error)
	GetDashboardStats() (map[string]interface{}, error)
	
	// Fulfill and Callback
	FulfillOrder(tx *domain.Transaction) error
	HandleDigiflazzCallback(payload *DigiflazzCallbackPayload) error
	HandlePaymentSuccess(invoiceNumber, paymentRef string, paidAmount float64) error
	
	// Admin overrides
	ManualRetry(transactionID uint) error
	ManualSetSuccess(transactionID uint, notes string) error
	ManualRefund(transactionID uint, notes string) error
}

type transactionService struct {
	txRepo           repository.TransactionRepository
	nominalRepo      repository.NominalRepository
	gameRepo         repository.GameRepository
	userRepo         repository.UserRepository
	paymentRepo      repository.PaymentRepository
	providerRepo     repository.ProviderRepository
	digiflazzBuyer   DigiflazzBuyerService
	kiosgamerService KiosgamerService
}

func NewTransactionService(
	txRepo repository.TransactionRepository,
	nominalRepo repository.NominalRepository,
	gameRepo repository.GameRepository,
	userRepo repository.UserRepository,
	paymentRepo repository.PaymentRepository,
	providerRepo repository.ProviderRepository,
	digiflazzBuyer DigiflazzBuyerService,
	kiosgamerService KiosgamerService,
) TransactionService {
	return &transactionService{
		txRepo:           txRepo,
		nominalRepo:      nominalRepo,
		gameRepo:         gameRepo,
		userRepo:         userRepo,
		paymentRepo:      paymentRepo,
		providerRepo:     providerRepo,
		digiflazzBuyer:   digiflazzBuyer,
		kiosgamerService: kiosgamerService,
	}
}

func (s *transactionService) CreateOrder(req *CreateOrderRequest) (*domain.Transaction, error) {
	nominal, err := s.nominalRepo.FindByID(req.NominalID)
	if err != nil || nominal == nil || !nominal.IsActive {
		return nil, errors.New("selected product is not active or available")
	}

	game, err := s.gameRepo.FindByID(req.GameID)
	if err != nil || game == nil || !game.IsActive {
		return nil, errors.New("game is not active")
	}

	// Determine price based on user tier
	var sellingPrice float64 = nominal.PricePublic
	var user *domain.User
	if req.UserID != nil && *req.UserID > 0 {
		user, _ = s.userRepo.FindByID(*req.UserID)
		if user != nil {
			switch user.Tier {
			case domain.TierVIP:
				sellingPrice = nominal.PriceVIP
			case domain.TierReseller:
				sellingPrice = nominal.PriceReseller
			case domain.TierMember:
				sellingPrice = nominal.PriceMember
			default:
				sellingPrice = nominal.PricePublic
			}
		}
	}

	// Calculate payment fee
	var adminFee float64 = 0
	paymentMethod, err := s.paymentRepo.GetByCode(req.PaymentMethod)
	if err == nil && paymentMethod != nil {
		adminFee = paymentMethod.CalculateFee(sellingPrice)
	}

	totalAmount := sellingPrice + adminFee
	invoiceNumber := utils.GenerateInvoiceNumber()
	refID := utils.GenerateRefID()

	// If paying with SALDO
	if req.PaymentMethod == "SALDO" {
		if user == nil {
			return nil, errors.New("login is required to pay with account balance")
		}
		if user.Balance < totalAmount {
			return nil, errors.New("insufficient balance")
		}

		// Deduct balance
		err = s.userRepo.UpdateBalance(user.ID, totalAmount, domain.MutationDebit, "TRANSACTION", invoiceNumber, fmt.Sprintf("Top up %s - %s", game.Name, nominal.Name))
		if err != nil {
			return nil, err
		}
	}

	tx := &domain.Transaction{
		InvoiceNumber:  invoiceNumber,
		Source:         domain.SourceWeb,
		UserID:         req.UserID,
		CustomerID:     req.CustomerID,
		ServerID:       req.ServerID,
		CustomerPhone:  req.CustomerPhone,
		CustomerEmail:  req.CustomerEmail,
		Nickname:       req.Nickname,
		GameID:         game.ID,
		NominalID:      nominal.ID,
		ProviderID:     nominal.ProviderID,
		BasePrice:      nominal.BasePrice,
		SellingPrice:   sellingPrice,
		AdminFee:       adminFee,
		TotalAmount:    totalAmount,
		Profit:         sellingPrice - nominal.BasePrice,
		Status:         domain.StatusPending,
		PaymentMethod:  req.PaymentMethod,
		RefID:          refID,
	}

	if req.PaymentMethod == "SALDO" {
		now := time.Now()
		tx.PaymentVerifiedAt = &now
		tx.Status = domain.StatusProcessing
	}

	if err := s.txRepo.Create(tx); err != nil {
		if req.PaymentMethod == "SALDO" && user != nil {
			_ = s.userRepo.UpdateBalance(user.ID, totalAmount, domain.MutationCredit, "REFUND", invoiceNumber, "Refund on create error")
		}
		return nil, err
	}

	// If paid with saldo, fulfill immediately via worker pool
	if req.PaymentMethod == "SALDO" {
		targetTx := tx
		worker.GlobalPool.Submit(func() {
			_ = s.FulfillOrder(targetTx)
		})
	}

	return tx, nil
}

func (s *transactionService) FulfillOrder(tx *domain.Transaction) error {
	nominal, err := s.nominalRepo.FindByID(tx.NominalID)
	if err != nil || nominal == nil {
		return errors.New("nominal not found")
	}

	// -------------------------------------------------------------------------
	// White-Label Margin Guard (Anti-Jual Rugi):
	// If the provider modal exceeds the customer's payment, do NOT fire provider.
	// Keep transaction in 'processing' safely so admin balance is protected.
	// -------------------------------------------------------------------------
	if nominal.BasePrice > tx.SellingPrice {
		tx.Status = domain.StatusProcessing
		tx.ProviderStatus = "Pending"
		tx.ProviderMessage = "Dalam antrean pemrosesan server"
		_ = s.txRepo.Update(tx)
		_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusProcessing, "Dalam antrean pemrosesan server")
		return nil
	}

	providerCode := "DIGIFLAZZ"
	if nominal.Provider != nil && nominal.Provider.Code != "" {
		providerCode = nominal.Provider.Code
	} else if nominal.ProviderID > 0 && s.providerRepo != nil {
		if p, err := s.providerRepo.GetByID(nominal.ProviderID); err == nil && p != nil {
			providerCode = p.Code
		}
	}

	if providerCode == "KIOSGAMER" {
		if s.kiosgamerService == nil {
			return errors.New("kiosgamer service is not initialized")
		}

		// Gunakan KiosgamerProductCode (item_id Kiosgamer), bukan ProviderProductCode (SKU Digiflazz)
		kiosgamerSKU := nominal.KiosgamerProductCode
		if kiosgamerSKU == "" {
			// Fallback: tandai gagal karena SKU Kiosgamer belum dimapping
			tx.Status = domain.StatusFailed
			tx.ProviderStatus = "Konfigurasi Error"
			tx.ProviderMessage = fmt.Sprintf("SKU Kiosgamer belum dikonfigurasi untuk produk '%s'. Silakan isi item_id di halaman Nominals atau gunakan Auto-Sync SKU di Kiosgamer Center.", nominal.Name)
			now := time.Now()
			tx.CompletedAt = &now
			_ = s.txRepo.Update(tx)
			_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusFailed, tx.ProviderMessage)
			// Auto-refund jika bayar dengan saldo
			if tx.PaymentMethod == "SALDO" && tx.UserID != nil {
				_ = s.userRepo.UpdateBalance(*tx.UserID, tx.TotalAmount, domain.MutationCredit, "REFUND", tx.InvoiceNumber, "Pengembalian dana: SKU Kiosgamer belum dikonfigurasi")
			}
			return errors.New(tx.ProviderMessage)
		}

		// Execute actual top-up via Kiosgamer menggunakan item_id yang benar
		// Ambil slug game untuk resolusi app_id yang akurat (FF vs CODM)
		gameSlug := ""
		if s.gameRepo != nil {
			if g, err := s.gameRepo.FindByID(tx.GameID); err == nil && g != nil {
				gameSlug = g.Slug
			}
		}

		var result *KiosgamerOrderResult
		// -----------------------------------------------------------------------
		// SAFE RETRY: Jika display_id sudah ada (order sudah dikirim ke Kiosgamer),
		// lanjutkan poll status TANPA membuat order baru agar shell tidak terpotong ganda.
		// -----------------------------------------------------------------------
		if tx.ProviderOrderID != "" && tx.ProviderOrderID != "-" {
			result, err = s.kiosgamerService.PollOrder(context.Background(), tx.ProviderOrderID)
		} else {
			result, err = s.kiosgamerService.PlaceOrder(
				context.Background(),
				tx.RefID,
				kiosgamerSKU,
				tx.CustomerID,
				tx.ServerID,
				gameSlug,
			)
		}
		if err != nil {
			tx.RetryCount++
			switch {
			case errors.Is(err, ErrKiosgamerChallengeRequired):
				tx.ProviderStatus = "Challenge Required"
				tx.ProviderMessage = fmt.Sprintf("Kiosgamer anti-bot challenge: %v", err)
			case errors.Is(err, ErrKiosgamerReauthRequired):
				tx.ProviderStatus = "Reauth Required"
				tx.ProviderMessage = fmt.Sprintf("Kiosgamer re-authentication required: %v", err)
			case errors.Is(err, ErrKiosgamerSessionExpired), errors.Is(err, ErrKiosgamerNotConfigured):
				tx.ProviderStatus = "Session Error"
				tx.ProviderMessage = fmt.Sprintf("Kiosgamer session error: %v", err)
			default:
				// Error non-session: player tidak ditemukan, produk tidak ada di Kiosgamer, dll.
				tx.ProviderStatus = "Provider Error"
				tx.ProviderMessage = fmt.Sprintf("Kiosgamer provider error: %v", err)
			}
			_ = s.txRepo.Update(tx)
			return err
		}

		// Map Kiosgamer result → transaction status
		if result.OrderID != "" {
			tx.ProviderOrderID = result.OrderID
		}
		tx.ProviderMessage = result.Message

		respJSON, _ := json.Marshal(result)
		tx.ProviderCallbackData = string(respJSON)

		switch result.Status {
		case "success":
			tx.Status = domain.StatusSuccess
			tx.ProviderStatus = "Sukses"
			tx.PaymentReference = result.SerialNumber
			now := time.Now()
			tx.CompletedAt = &now
			_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusSuccess, "Kiosgamer: top up berhasil diproses")
			sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
				"status": "success", "invoice": tx.InvoiceNumber, "completed_at": now,
			})

		case "failed":
			tx.Status = domain.StatusFailed
			tx.ProviderStatus = "Gagal"
			now := time.Now()
			tx.CompletedAt = &now
			_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusFailed, fmt.Sprintf("Kiosgamer gagal: %s", result.Message))
			// Auto-refund if paid with balance
			if tx.PaymentMethod == "SALDO" && tx.UserID != nil {
				_ = s.userRepo.UpdateBalance(*tx.UserID, tx.TotalAmount, domain.MutationCredit, "REFUND", tx.InvoiceNumber, "Pengembalian dana: top up Kiosgamer gagal")
			}
			sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
				"status": "failed", "invoice": tx.InvoiceNumber,
			})

		default: // pending or unknown
			tx.Status = domain.StatusProcessing
			tx.ProviderStatus = "Pending"
			_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusProcessing, "Kiosgamer: pesanan sedang diproses")
			sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
				"status": "processing", "invoice": tx.InvoiceNumber,
			})
		}

		return s.txRepo.Update(tx)
	}


	// Call Digiflazz Buyer API
	resp, err := s.digiflazzBuyer.CreateTransaction(tx.RefID, nominal.ProviderProductCode, tx.CustomerID, false)
	if err != nil {
		tx.RetryCount++
		tx.ProviderMessage = err.Error()
		errJSON, _ := json.Marshal(map[string]interface{}{
			"error":     err.Error(),
			"ref_id":    tx.RefID,
			"timestamp": time.Now().Format(time.RFC3339),
		})
		tx.ProviderCallbackData = string(errJSON)
		_ = s.txRepo.Update(tx)
		return err
	}

	tx.ProviderStatus = resp.Data.Status
	tx.ProviderMessage = resp.Data.Message
	tx.ProviderOrderID = resp.Data.RefID
	tx.PaymentReference = resp.Data.SN

	respJSON, _ := json.Marshal(resp.Data)
	tx.ProviderCallbackData = string(respJSON)

	if resp.Data.Status == "Sukses" {
		tx.Status = domain.StatusSuccess
		now := time.Now()
		tx.CompletedAt = &now
		_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusSuccess, "Provider completed transaction successfully")
		sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
			"status": "success", "invoice": tx.InvoiceNumber, "completed_at": now,
		})
	} else if resp.Data.Status == "Gagal" {
		tx.Status = domain.StatusFailed
		now := time.Now()
		tx.CompletedAt = &now
		_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusFailed, fmt.Sprintf("Provider failed: %s", resp.Data.Message))
		// Auto refund if paid with SALDO
		if tx.PaymentMethod == "SALDO" && tx.UserID != nil {
			_ = s.userRepo.UpdateBalance(*tx.UserID, tx.TotalAmount, domain.MutationCredit, "REFUND", tx.InvoiceNumber, "Pengembalian dana top up gagal")
		}
		sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
			"status": "failed", "invoice": tx.InvoiceNumber, "completed_at": now,
		})
	} else {
		tx.Status = domain.StatusProcessing
		_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusProcessing, "Waiting for provider callback")
		sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
			"status": "processing", "invoice": tx.InvoiceNumber,
		})
	}

	return s.txRepo.Update(tx)
}

func (s *transactionService) HandleDigiflazzCallback(payload *DigiflazzCallbackPayload) error {
	if payload == nil || payload.Data.RefID == "" {
		return errors.New("empty callback data")
	}

	tx, err := s.txRepo.FindByRefID(payload.Data.RefID)
	if err != nil || tx == nil {
		// Try by invoice number
		tx, err = s.txRepo.FindByInvoiceNumber(payload.Data.RefID)
	}
	if err != nil || tx == nil {
		return errors.New("transaction not found for callback")
	}

	// Idempotency: skip if already final
	if tx.Status == domain.StatusSuccess || tx.Status == domain.StatusFailed {
		return nil
	}

	status := payload.Data.Status
	tx.ProviderStatus = status
	tx.ProviderMessage = payload.Data.Message
	tx.PaymentReference = payload.Data.SN

	callbackJSON, _ := json.Marshal(payload.Data)
	tx.ProviderCallbackData = string(callbackJSON)

	if status == "Sukses" {
		tx.Status = domain.StatusSuccess
		now := time.Now()
		tx.CompletedAt = &now
		_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusSuccess, "Digiflazz callback: Sukses")
		sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
			"status": "success", "invoice": tx.InvoiceNumber, "completed_at": now,
		})
	} else if status == "Gagal" {
		tx.Status = domain.StatusFailed
		now := time.Now()
		tx.CompletedAt = &now
		_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusFailed, fmt.Sprintf("Digiflazz callback: %s", payload.Data.Message))
		// Refund if paid with saldo
		if tx.PaymentMethod == "SALDO" && tx.UserID != nil {
			_ = s.userRepo.UpdateBalance(*tx.UserID, tx.TotalAmount, domain.MutationCredit, "REFUND", tx.InvoiceNumber, "Pengembalian dana callback gagal")
		}
		sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
			"status": "failed", "invoice": tx.InvoiceNumber, "completed_at": now,
		})
	}

	return s.txRepo.Update(tx)
}

func (s *transactionService) HandlePaymentSuccess(invoiceNumber, paymentRef string, paidAmount float64) error {
	tx, err := s.txRepo.FindByInvoiceNumber(invoiceNumber)
	if err != nil || tx == nil {
		return errors.New("transaction not found")
	}

	// Cross-check the amount the payment gateway says was paid against what
	// this transaction actually expects. A mismatch is never fulfilled —
	// this is a second line of defense independent of Tripay's own
	// "value consistency" setting (which we deliberately leave off).
	// A small epsilon guards against float64 rounding, not real discrepancies.
	const amountEpsilon = 1.0 // rupiah; adjust if gateway sends fractional units
	if paidAmount > 0 && math.Abs(paidAmount-tx.TotalAmount) > amountEpsilon {
		_ = s.txRepo.UpdateStatus(tx.ID, domain.StatusFailed,
			fmt.Sprintf("Amount mismatch: expected %.2f, gateway reported %.2f", tx.TotalAmount, paidAmount))
		return fmt.Errorf("amount mismatch for invoice %s: expected %.2f, got %.2f", invoiceNumber, tx.TotalAmount, paidAmount)
	}

	// Atomically flip Pending -> Processing. If another (duplicate/retried)
	// callback already did this, ok is false and we stop here — this is
	// what makes concurrent duplicate webhooks safe.
	ok, err := s.txRepo.MarkAsProcessingIfPending(tx.ID, paymentRef, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		return nil // Already processed by a prior/concurrent callback
	}

	// Broadcast ke browser pembeli: pembayaran diterima, sedang diproses
	sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
		"status": "processing", "invoice": tx.InvoiceNumber,
	})

	// Fulfill order via worker pool
	targetTx := tx
	worker.GlobalPool.Submit(func() {
		_ = s.FulfillOrder(targetTx)
	})

	return nil
}

func (s *transactionService) GetByInvoice(invoice string) (*domain.Transaction, error) {
	return s.txRepo.FindByInvoiceNumber(invoice)
}

func (s *transactionService) ListRecent(limit int) ([]domain.Transaction, error) {
	return s.txRepo.ListRecent(limit)
}

func (s *transactionService) ListUserTransactions(userID uint, offset, limit int) ([]domain.Transaction, int64, error) {
	return s.txRepo.ListByUser(userID, offset, limit)
}

func (s *transactionService) ListAdminTransactions(offset, limit int, status, search, startDate, endDate string) ([]domain.Transaction, int64, error) {
	return s.txRepo.ListAdmin(offset, limit, status, search, startDate, endDate)
}

func (s *transactionService) GetDashboardStats() (map[string]interface{}, error) {
	return s.txRepo.GetDashboardStats()
}

func (s *transactionService) ManualRetry(transactionID uint) error {
	tx, err := s.txRepo.FindByID(transactionID)
	if err != nil || tx == nil {
		return errors.New("transaction not found")
	}

	// Reset status ke Processing agar FulfillOrder dipanggil ulang.
	// PENTING: ProviderOrderID TIDAK di-reset agar FulfillOrder bisa melanjutkan
	// poll order yang sudah ada di Kiosgamer, bukan membuat order baru (double-charge).
	tx.Status = domain.StatusProcessing
	tx.ProviderStatus = "Retrying"
	tx.ProviderMessage = "Transaksi sedang diproses ulang oleh admin"
	_ = s.txRepo.Update(tx)

	targetTx := tx
	worker.GlobalPool.Submit(func() {
		_ = s.FulfillOrder(targetTx)
	})

	return nil
}

func (s *transactionService) ManualSetSuccess(transactionID uint, notes string) error {
	tx, err := s.txRepo.FindByID(transactionID)
	if err != nil || tx == nil {
		return errors.New("transaction not found")
	}

	if tx.Status == domain.StatusSuccess {
		return errors.New("transaction is already marked as success")
	}

	// If paid with SALDO / SALDO_H2H and was previously failed/refunded,
	// the funds were already refunded to the user. We must re-deduct the balance!
	if (tx.PaymentMethod == "SALDO" || tx.PaymentMethod == "SALDO_H2H") && tx.UserID != nil {
		if tx.Status == domain.StatusFailed || tx.Status == domain.StatusRefunded {
			user, err := s.userRepo.FindByID(*tx.UserID)
			if err != nil || user == nil {
				return errors.New("user account not found")
			}
			if user.Balance < tx.TotalAmount {
				return fmt.Errorf("saldo akun tidak mencukupi untuk dipotong kembali (Saldo saat ini: Rp %.0f, Dibutuhkan: Rp %.0f)", user.Balance, tx.TotalAmount)
			}
			err = s.userRepo.UpdateBalance(*tx.UserID, tx.TotalAmount, domain.MutationDebit, "TRANSACTION_MANUAL_RECOVERY", tx.InvoiceNumber, fmt.Sprintf("Pemotongan kembali saldo transaksi %s (Sukses manual oleh admin)", tx.InvoiceNumber))
			if err != nil {
				return fmt.Errorf("gagal memotong saldo: %w", err)
			}
		}
	}

	now := time.Now()
	tx.Status = domain.StatusSuccess
	tx.CompletedAt = &now
	if tx.PaymentVerifiedAt == nil {
		tx.PaymentVerifiedAt = &now
	}
	if notes != "" {
		tx.ProviderMessage = "Manual success: " + notes
		if tx.PaymentReference == "" {
			tx.PaymentReference = notes
		}
	}

	manualSuccessJSON, _ := json.Marshal(map[string]interface{}{
		"source":        "ADMIN_MANUAL_ACTION",
		"status":        "Sukses",
		"sn":            tx.PaymentReference,
		"notes":         notes,
		"completed_at":  now.Format(time.RFC3339),
	})
	tx.ProviderCallbackData = string(manualSuccessJSON)

	_ = s.txRepo.Update(tx)
	err = s.txRepo.UpdateStatus(transactionID, domain.StatusSuccess, fmt.Sprintf("Manual success by admin: %s", notes))
	sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
		"status": "success", "invoice": tx.InvoiceNumber, "completed_at": tx.CompletedAt,
	})
	return err
}

func (s *transactionService) ManualRefund(transactionID uint, notes string) error {
	tx, err := s.txRepo.FindByID(transactionID)
	if err != nil || tx == nil {
		return errors.New("transaction not found")
	}

	if tx.Status == domain.StatusRefunded {
		return errors.New("transaction already refunded")
	}

	if tx.UserID != nil {
		_ = s.userRepo.UpdateBalance(*tx.UserID, tx.TotalAmount, domain.MutationCredit, "REFUND", tx.InvoiceNumber, fmt.Sprintf("Manual refund by admin: %s", notes))
	}

	now := time.Now()
	tx.Status = domain.StatusRefunded
	tx.CompletedAt = &now

	refundJSON, _ := json.Marshal(map[string]interface{}{
		"source":        "ADMIN_MANUAL_REFUND",
		"status":        "Refunded",
		"notes":         notes,
		"completed_at":  now.Format(time.RFC3339),
	})
	tx.ProviderCallbackData = string(refundJSON)
	_ = s.txRepo.Update(tx)

	err = s.txRepo.UpdateStatus(transactionID, domain.StatusRefunded, fmt.Sprintf("Refunded by admin: %s", notes))
	sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
		"status": "refunded", "invoice": tx.InvoiceNumber, "completed_at": now,
	})
	return err
}