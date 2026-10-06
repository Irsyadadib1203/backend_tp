package repository

import (
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"topup-backend/internal/domain"
)

type ProviderProductRepository interface {
	Create(pp *domain.ProviderProduct) error
	Upsert(pp *domain.ProviderProduct) error
	FindByNominalAndProvider(nominalID, providerID uint) (*domain.ProviderProduct, error)
	FindByNominalAndProviderCode(nominalID uint, providerCode string) (*domain.ProviderProduct, error)
	ListByNominalID(nominalID uint) ([]domain.ProviderProduct, error)
	ListByProviderID(providerID uint) ([]domain.ProviderProduct, error)
	Delete(id uint) error
}

type providerProductRepository struct {
	db *gorm.DB
}

func NewProviderProductRepository(db *gorm.DB) ProviderProductRepository {
	return &providerProductRepository{db: db}
}

func (r *providerProductRepository) Create(pp *domain.ProviderProduct) error {
	return r.db.Create(pp).Error
}

func (r *providerProductRepository) Upsert(pp *domain.ProviderProduct) error {
	if pp == nil {
		return errors.New("provider product is nil")
	}
	// Use clause.OnConflict to handle unique (nominal_id, provider_id)
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "nominal_id"}, {Name: "provider_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"product_code",
			"cost_price",
			"priority",
			"is_active",
			"extra",
			"updated_at",
		}),
	}).Create(pp).Error
}

func (r *providerProductRepository) FindByNominalAndProvider(nominalID, providerID uint) (*domain.ProviderProduct, error) {
	var pp domain.ProviderProduct
	err := r.db.Preload("Provider").
		Where("nominal_id = ? AND provider_id = ? AND is_active = ?", nominalID, providerID, true).
		First(&pp).Error
	if err != nil {
		return nil, err
	}
	return &pp, nil
}

func (r *providerProductRepository) FindByNominalAndProviderCode(nominalID uint, providerCode string) (*domain.ProviderProduct, error) {
	var pp domain.ProviderProduct
	err := r.db.Joins("JOIN providers ON providers.id = provider_products.provider_id").
		Where("provider_products.nominal_id = ? AND UPPER(providers.code) = ? AND provider_products.is_active = ?",
			nominalID, strings.ToUpper(strings.TrimSpace(providerCode)), true).
		Preload("Provider").
		First(&pp).Error
	if err != nil {
		return nil, err
	}
	return &pp, nil
}

func (r *providerProductRepository) ListByNominalID(nominalID uint) ([]domain.ProviderProduct, error) {
	var list []domain.ProviderProduct
	err := r.db.Preload("Provider").
		Where("nominal_id = ?", nominalID).
		Order("priority DESC, id ASC").
		Find(&list).Error
	return list, err
}

func (r *providerProductRepository) ListByProviderID(providerID uint) ([]domain.ProviderProduct, error) {
	var list []domain.ProviderProduct
	err := r.db.Where("provider_id = ?", providerID).
		Order("nominal_id ASC").
		Find(&list).Error
	return list, err
}

func (r *providerProductRepository) Delete(id uint) error {
	return r.db.Delete(&domain.ProviderProduct{}, id).Error
}
