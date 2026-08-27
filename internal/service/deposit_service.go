package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"topup-backend/internal/domain"
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
}

type depositService struct {
	depositRepo repository.DepositRepository
	userRepo    repository.UserRepository
}

func NewDepositService(depositRepo repository.DepositRepository, userRepo repository.UserRepository) DepositService {
	return &depositService{
		depositRepo: depositRepo,
		userRepo:    userRepo,
	}
}

func (s *depositService) CreateDeposit(userID uint, amount float64, paymentMethod string) (*domain.DepositRequest, error) {
	if amount < 10000 {
		return nil, errors.New("minimum deposit amount is Rp 10.000")
	}

	user, err := s.userRepo.FindByID(userID)
	if err != nil || user == nil {
		return nil, errors.New("user not found")
	}

	// Generate 3 digit unique code
	codeInt, _ := rand.Int(rand.Reader, big.NewInt(900))
	uniqueCode := int(codeInt.Int64()) + 100 // 100 - 999

	totalAmount := amount
	if paymentMethod != "SALDO" && paymentMethod != "QRIS" {
		totalAmount = amount + float64(uniqueCode)
	}

	invoice := fmt.Sprintf("DEP-%s-%s", time.Now().Format("20060102"), utils.GenerateRandomString(5))

	req := &domain.DepositRequest{
		InvoiceNumber: invoice,
		UserID:        userID,
		Amount:        amount,
		UniqueCode:    uniqueCode,
		TotalAmount:   totalAmount,
		PaymentMethod: paymentMethod,
		Status:        domain.DepositPending,
	}

	if err := s.depositRepo.Create(req); err != nil {
		return nil, err
	}

	return req, nil
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
