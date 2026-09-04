package repository

import (
	"time"

	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type DepositRepository interface {
	Create(deposit *domain.DepositRequest) error
	FindByID(id uint) (*domain.DepositRequest, error)
	FindByInvoice(invoice string) (*domain.DepositRequest, error)
	Update(deposit *domain.DepositRequest) error
	ListByUser(userID uint, offset, limit int) ([]domain.DepositRequest, int64, error)
	ListAdmin(offset, limit int, status string) ([]domain.DepositRequest, int64, error)
	ListMutations(userID uint, offset, limit int) ([]domain.BalanceMutation, int64, error)
	MarkAsApprovedIfPending(id uint, tripayRef string, approvedAt time.Time) (bool, error)
}

type depositRepository struct {
	db *gorm.DB
}

func NewDepositRepository(db *gorm.DB) DepositRepository {
	return &depositRepository{db: db}
}

func (r *depositRepository) Create(deposit *domain.DepositRequest) error {
	return r.db.Create(deposit).Error
}

func (r *depositRepository) FindByID(id uint) (*domain.DepositRequest, error) {
	var dep domain.DepositRequest
	err := r.db.Preload("User").First(&dep, id).Error
	if err != nil {
		return nil, err
	}
	return &dep, nil
}

func (r *depositRepository) FindByInvoice(invoice string) (*domain.DepositRequest, error) {
	var dep domain.DepositRequest
	err := r.db.Preload("User").Where("invoice_number = ?", invoice).First(&dep).Error
	if err != nil {
		return nil, err
	}
	return &dep, nil
}

func (r *depositRepository) Update(deposit *domain.DepositRequest) error {
	return r.db.Save(deposit).Error
}

func (r *depositRepository) ListByUser(userID uint, offset, limit int) ([]domain.DepositRequest, int64, error) {
	var deps []domain.DepositRequest
	var total int64
	query := r.db.Model(&domain.DepositRequest{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&deps).Error
	return deps, total, err
}

func (r *depositRepository) ListAdmin(offset, limit int, status string) ([]domain.DepositRequest, int64, error) {
	var deps []domain.DepositRequest
	var total int64
	query := r.db.Model(&domain.DepositRequest{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Preload("User").Order("id DESC").Offset(offset).Limit(limit).Find(&deps).Error
	return deps, total, err
}

func (r *depositRepository) ListMutations(userID uint, offset, limit int) ([]domain.BalanceMutation, int64, error) {
	var mutations []domain.BalanceMutation
	var total int64
	query := r.db.Model(&domain.BalanceMutation{})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Preload("User").Order("id DESC").Offset(offset).Limit(limit).Find(&mutations).Error
	return mutations, total, err
}

func (r *depositRepository) MarkAsApprovedIfPending(id uint, tripayRef string, approvedAt time.Time) (bool, error) {
	updates := map[string]interface{}{
		"status":      domain.DepositApproved,
		"approved_at": approvedAt,
	}
	if tripayRef != "" {
		updates["tripay_reference"] = tripayRef
	}
	result := r.db.Model(&domain.DepositRequest{}).
		Where("id = ? AND status = ?", id, domain.DepositPending).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
