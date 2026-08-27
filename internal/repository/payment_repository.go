package repository

import (
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type PaymentRepository interface {
	ListActive() ([]domain.PaymentMethod, error)
	ListAll() ([]domain.PaymentMethod, error)
	GetByCode(code string) (*domain.PaymentMethod, error)
	GetByID(id uint) (*domain.PaymentMethod, error)
	Create(pm *domain.PaymentMethod) error
	Update(pm *domain.PaymentMethod) error
	Delete(id uint) error
}

type paymentRepository struct {
	db *gorm.DB
}

func NewPaymentRepository(db *gorm.DB) PaymentRepository {
	return &paymentRepository{db: db}
}

func (r *paymentRepository) ListActive() ([]domain.PaymentMethod, error) {
	var methods []domain.PaymentMethod
	err := r.db.Where("is_active = ?", true).Order("sort_order ASC, name ASC").Find(&methods).Error
	return methods, err
}

func (r *paymentRepository) ListAll() ([]domain.PaymentMethod, error) {
	var methods []domain.PaymentMethod
	err := r.db.Order("sort_order ASC, name ASC").Find(&methods).Error
	return methods, err
}

func (r *paymentRepository) GetByCode(code string) (*domain.PaymentMethod, error) {
	var method domain.PaymentMethod
	err := r.db.Where("code = ?", code).First(&method).Error
	return &method, err
}

func (r *paymentRepository) GetByID(id uint) (*domain.PaymentMethod, error) {
	var method domain.PaymentMethod
	err := r.db.First(&method, id).Error
	return &method, err
}

func (r *paymentRepository) Create(pm *domain.PaymentMethod) error {
	return r.db.Create(pm).Error
}

func (r *paymentRepository) Update(pm *domain.PaymentMethod) error {
	return r.db.Save(pm).Error
}

func (r *paymentRepository) Delete(id uint) error {
	return r.db.Delete(&domain.PaymentMethod{}, id).Error
}
