package repository

import (
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type ProviderRepository interface {
	GetByCode(code string) (*domain.Provider, error)
	GetByID(id uint) (*domain.Provider, error)
	List() ([]domain.Provider, error)
	Update(provider *domain.Provider) error
	UpdateBalance(id uint, balance float64) error
	LogWebhook(log *domain.WebhookLog) error
	ListWebhookLogs(offset, limit int, provider string) ([]domain.WebhookLog, int64, error)
}

type providerRepository struct {
	db *gorm.DB
}

func NewProviderRepository(db *gorm.DB) ProviderRepository {
	return &providerRepository{db: db}
}

func (r *providerRepository) GetByCode(code string) (*domain.Provider, error) {
	var provider domain.Provider
	err := r.db.Where("code = ?", code).First(&provider).Error
	return &provider, err
}

func (r *providerRepository) GetByID(id uint) (*domain.Provider, error) {
	var provider domain.Provider
	err := r.db.First(&provider, id).Error
	return &provider, err
}

func (r *providerRepository) List() ([]domain.Provider, error) {
	var providers []domain.Provider
	err := r.db.Find(&providers).Error
	return providers, err
}

func (r *providerRepository) Update(provider *domain.Provider) error {
	return r.db.Save(provider).Error
}

func (r *providerRepository) UpdateBalance(id uint, balance float64) error {
	return r.db.Model(&domain.Provider{}).Where("id = ?", id).Update("balance", balance).Error
}

func (r *providerRepository) LogWebhook(log *domain.WebhookLog) error {
	return r.db.Create(log).Error
}

func (r *providerRepository) ListWebhookLogs(offset, limit int, provider string) ([]domain.WebhookLog, int64, error) {
	var logs []domain.WebhookLog
	var total int64
	query := r.db.Model(&domain.WebhookLog{})
	if provider != "" {
		query = query.Where("provider_name = ?", provider)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&logs).Error
	return logs, total, err
}
