package repository

import (
	"topup-backend/internal/domain"

	"gorm.io/gorm"
)

type BannerRepository interface {
	List(activeOnly bool) ([]domain.Banner, error)
	GetByID(id uint) (*domain.Banner, error)
	Create(b *domain.Banner) error
	Update(b *domain.Banner) error
	Delete(id uint) error
}

type bannerRepository struct {
	db *gorm.DB
}

func NewBannerRepository(db *gorm.DB) BannerRepository {
	return &bannerRepository{db: db}
}

func (r *bannerRepository) List(activeOnly bool) ([]domain.Banner, error) {
	var banners []domain.Banner
	q := r.db.Order("sort_order ASC, id ASC")
	if activeOnly {
		q = q.Where("is_active = ?", true)
	}
	err := q.Find(&banners).Error
	return banners, err
}

func (r *bannerRepository) GetByID(id uint) (*domain.Banner, error) {
	var b domain.Banner
	if err := r.db.First(&b, id).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *bannerRepository) Create(b *domain.Banner) error {
	return r.db.Create(b).Error
}

func (r *bannerRepository) Update(banner *domain.Banner) error {
    updates := map[string]interface{}{
        "title":      banner.Title,
        "subtitle":   banner.Subtitle,
        "image_url":  banner.ImageURL,
        "link_url":   banner.LinkURL,
        "badge_text": banner.BadgeText,
        "sort_order": banner.SortOrder,
        "is_active":  banner.IsActive,
    }

    return r.db.
        Model(&domain.Banner{}).
        Where("id = ?", banner.ID).
        Updates(updates).
        Error
}

func (r *bannerRepository) Delete(id uint) error {
	return r.db.Delete(&domain.Banner{}, id).Error
}
