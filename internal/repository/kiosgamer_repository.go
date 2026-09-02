package repository

import (
	"errors"

	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type KiosgamerRepository interface {
	GetByProviderID(providerID uint) (*domain.KiosgamerCredential, error)
	Upsert(credential *domain.KiosgamerCredential) error
}

type kiosgamerRepository struct {
	db *gorm.DB
}

func NewKiosgamerRepository(db *gorm.DB) KiosgamerRepository {
	return &kiosgamerRepository{db: db}
}

func (r *kiosgamerRepository) GetByProviderID(providerID uint) (*domain.KiosgamerCredential, error) {
	var credential domain.KiosgamerCredential
	err := r.db.Where("provider_id = ?", providerID).First(&credential).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &credential, err
}

func (r *kiosgamerRepository) Upsert(credential *domain.KiosgamerCredential) error {
	var existing domain.KiosgamerCredential
	err := r.db.Where("provider_id = ?", credential.ProviderID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.Create(credential).Error
	}
	if err != nil {
		return err
	}

	credential.ID = existing.ID
	credential.CreatedAt = existing.CreatedAt
	return r.db.Save(credential).Error
}
