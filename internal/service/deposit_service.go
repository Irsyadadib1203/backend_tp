package service

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/sse"
	"topup-backend/internal/pkg/utils"
	"topup-backend/internal/repository"
)

type DepositService interface {
	CreateDeposit(userID uint, amount float64, paymentMethod string) (*domain.DepositRequest, error)
	ApproveDeposit(depositID uint, adminID uint) error
	RejectDeposit(depositID uint, adminID uint, notes string) error
	GetUserDeposits(userID uint, offset, limit int) ([]domain.DepositRequest, int64, error)
	GetAdminDeposits(offset, limit int, status string) ([]domain.DepositRequest, int64, error)
	GetMutations(userID uint, offset, limit int) ([]domain.BalanceMutation, int64, error)
	HandleTripayDepositSuccess(invoiceNumber, paymentRef string, paidAmount float64) error
	SetTripayService(tripayService TripayChannelService)
}

type depositService struct {
	depositRepo   repository.DepositRepository
	userRepo      repository.UserRepository
	paymentRepo   repository.PaymentRepository
	tripayService TripayChannelService
}

func NewDepositService(
	depositRepo repository.DepositRepository,
	userRepo repository.UserRepository,
	paymentRepo repository.PaymentRepository,
	tripayService TripayChannelService,
) DepositService {
	return &depositService{
		depositRepo:   depositRepo,
		userRepo:      userRepo,
		paymentRepo:   paymentRepo,
		tripayService: tripayService,
	}
}

func (s *depositService) SetTripayService(tripayService TripayChannelService) {
	s.tripayService = tripayService
}

func (s *depositService) CreateDeposit(userID uint, amount float64, paymentMethod string) (*domain.DepositRequest, error) {
	if amount < 10000 {
		return nil, errors.New("minimum deposit amount is Rp 10.000")
	}

	user, err := s.userRepo.FindByID(userID)
	if err != nil || user == nil {
		return nil, errors.New("user not found")
	}

	invoice := fmt.Sprintf("DEP-%s-%s", time.Now().Format("20060102"), utils.GenerateRandomString(5))
	isManual := strings.HasPrefix(paymentMethod, "MANUAL_")

	var uniqueCode int = 0
	var adminFee float64 = 0
	var totalAmount float64 = amount
	var paymentType string = "instant"
	var notes string = ""

	if isManual {
		paymentType = "manual"
		// Generate 3 digit unique code (100 - 999)
		codeInt, _ := rand.Int(rand.Reader, big.NewInt(900))
		uniqueCode = int(codeInt.Int64()) + 100
		adminFee = 0
		totalAmount = amount + float64(uniqueCode)
		bankName := strings.TrimPrefix(paymentMethod, "MANUAL_")
		notes = fmt.Sprintf("Transfer tepat Rp %.0f ke rekening %s (Termasuk kode unik)", totalAmount, bankName)
	} else {
		// Jalur Instan via Payment Gateway (Tripay)
		paymentType = "instant"
		if s.paymentRepo != nil {
			if pm, err := s.paymentRepo.GetByCode(paymentMethod); err == nil && pm != nil {
				adminFee = pm.CalculateFee(amount)
			}
		}
		totalAmount = amount + adminFee
	}

	req := &domain.DepositRequest{
		InvoiceNumber: invoice,
		UserID:        userID,
		Amount:        amount,
		UniqueCode:    uniqueCode,
		AdminFee:      adminFee,
		TotalAmount:   totalAmount,
		PaymentType:   paymentType,
		PaymentMethod: paymentMethod,
		Status:        domain.DepositPending,
		Notes:         notes,
	}

	// Jika jalur instan dan tripayService aktif, buat transaksi di Tripay
	if !isManual && s.tripayService != nil {
		custName := strings.TrimSpace(user.Name)
		if custName == "" {
			custName = "Member"
		}
		custEmail := strings.TrimSpace(user.Email)
		if custEmail == "" {
			custEmail = "member@example.com"
		}
		custPhone := strings.TrimSpace(user.PhoneNumber)
		if custPhone == "" {
			custPhone = "081234567890"
		}

		tripayReq := &TripayCreateTxRequest{
			Method:        paymentMethod,
			MerchantRef:   invoice,
			Amount:        int64(math.Round(totalAmount)),
			CustomerName:  custName,
			CustomerEmail: custEmail,
			CustomerPhone: custPhone,
			OrderItems: []TripayOrderItem{
				{
					SKU:      "DEPOSIT",
					Name:     fmt.Sprintf("Deposit Saldo Rp %.0f", amount),
					Price:    int64(math.Round(amount)),
					Quantity: 1,
				},
			},
			ExpiredTime: time.Now().Add(24 * time.Hour).Unix(),
		}

		tripayDetail, err := s.tripayService.CreateTransaction(tripayReq)
		if err != nil {
			log.Printf("[TripayDeposit] Failed to create transaction for invoice %s: %v", invoice, err)
			return nil, fmt.Errorf("gagal membuat transaksi deposit Tripay: %w", err)
		}

		if tripayDetail != nil {
			req.TripayReference = tripayDetail.Reference
			if tripayDetail.PayCode != "" {
				req.PaymentReference = tripayDetail.PayCode
			} else if tripayDetail.CheckoutURL != "" {
				req.PaymentReference = tripayDetail.CheckoutURL
			}
			req.CheckoutURL = tripayDetail.CheckoutURL
			if tripayDetail.PayURL != "" && req.CheckoutURL == "" {
				req.CheckoutURL = tripayDetail.PayURL
			}
			req.QRURL = tripayDetail.QRURL
			if len(tripayDetail.Instructions) > 0 {
				instrBytes, _ := json.Marshal(tripayDetail.Instructions)
				req.PaymentInstructions = string(instrBytes)
			}
		}
	}

	if err := s.depositRepo.Create(req); err != nil {
		return nil, err
	}

	return req, nil
}

func (s *depositService) HandleTripayDepositSuccess(invoiceNumber, paymentRef string, paidAmount float64) error {
	deposit, err := s.depositRepo.FindByInvoice(invoiceNumber)
	if err != nil || deposit == nil {
		return errors.New("deposit request not found")
	}

	if deposit.Status != domain.DepositPending {
		return nil // Already approved
	}

	const amountEpsilon = 1.0
	if paidAmount > 0 && math.Abs(paidAmount-deposit.TotalAmount) > amountEpsilon {
		log.Printf("[Deposit] Amount mismatch for invoice %s: expected %.2f, got %.2f", invoiceNumber, deposit.TotalAmount, paidAmount)
		return fmt.Errorf("amount mismatch: expected %.2f, got %.2f", deposit.TotalAmount, paidAmount)
	}

	now := time.Now()
	ok, err := s.depositRepo.MarkAsApprovedIfPending(deposit.ID, paymentRef, now)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	// Add balance to user account
	err = s.userRepo.UpdateBalance(
		deposit.UserID,
		deposit.Amount,
		domain.MutationCredit,
		"DEPOSIT",
		deposit.InvoiceNumber,
		fmt.Sprintf("Deposit saldo otomatis via %s (Invoice: %s)", deposit.PaymentMethod, deposit.InvoiceNumber),
	)
	if err != nil {
		log.Printf("[Deposit] Failed to credit balance for user %d: %v", deposit.UserID, err)
		return err
	}

	// Broadcast SSE update
	sse.GlobalHub.Broadcast(deposit.InvoiceNumber, "deposit_update", map[string]interface{}{
		"status":  "approved",
		"invoice": deposit.InvoiceNumber,
		"amount":  deposit.Amount,
	})

	return nil
}

func (s *depositService) ApproveDeposit(depositID uint, adminID uint) error {
	deposit, err := s.depositRepo.FindByID(depositID)
	if err != nil || deposit == nil {
		return errors.New("deposit request not found")
	}

	if deposit.Status != domain.DepositPending {
		return errors.New("deposit already processed")
	}

	now := time.Now()
	deposit.Status = domain.DepositApproved
	deposit.ApprovedBy = &adminID
	deposit.ApprovedAt = &now

	if err := s.depositRepo.Update(deposit); err != nil {
		return err
	}

	// Add balance to user account with mutation log
	return s.userRepo.UpdateBalance(
		deposit.UserID,
		deposit.Amount,
		domain.MutationCredit,
		"DEPOSIT",
		deposit.InvoiceNumber,
		fmt.Sprintf("Top up saldo via %s (Invoice: %s)", deposit.PaymentMethod, deposit.InvoiceNumber),
	)
}

func (s *depositService) RejectDeposit(depositID uint, adminID uint, notes string) error {
	deposit, err := s.depositRepo.FindByID(depositID)
	if err != nil || deposit == nil {
		return errors.New("deposit request not found")
	}

	if deposit.Status != domain.DepositPending {
		return errors.New("deposit already processed")
	}

	now := time.Now()
	deposit.Status = domain.DepositRejected
	deposit.ApprovedBy = &adminID
	deposit.ApprovedAt = &now
	deposit.Notes = notes

	return s.depositRepo.Update(deposit)
}

func (s *depositService) GetUserDeposits(userID uint, offset, limit int) ([]domain.DepositRequest, int64, error) {
	return s.depositRepo.ListByUser(userID, offset, limit)
}

func (s *depositService) GetAdminDeposits(offset, limit int, status string) ([]domain.DepositRequest, int64, error) {
	return s.depositRepo.ListAdmin(offset, limit, status)
}

func (s *depositService) GetMutations(userID uint, offset, limit int) ([]domain.BalanceMutation, int64, error) {
	return s.depositRepo.ListMutations(userID, offset, limit)
}
