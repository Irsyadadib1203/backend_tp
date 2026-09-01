package repository

import (
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type GameRepository interface {
	Create(game *domain.Game) error
	FindByID(id uint) (*domain.Game, error)
	FindBySlug(slug string) (*domain.Game, error)
	Update(game *domain.Game) error
	Delete(id uint) error
	ListPublic() ([]domain.Game, error)
	ListAdmin(offset, limit int, search, category string) ([]domain.Game, int64, error)
	SaveProviderMapping(mapping *domain.GameProvider) error
	GetProviderMappings(gameID uint) ([]domain.GameProvider, error)
}

type gameRepository struct {
	db *gorm.DB
}

func NewGameRepository(db *gorm.DB) GameRepository {
	return &gameRepository{db: db}
}

func (r *gameRepository) Create(game *domain.Game) error {
	return r.db.Create(game).Error
}

func (r *gameRepository) FindByID(id uint) (*domain.Game, error) {
	var game domain.Game
	err := r.db.Preload("Nominals", func(db *gorm.DB) *gorm.DB {
		return db.Where("is_active = ?", true).Order("sort_order ASC, base_price ASC")
	}).Preload("Providers").First(&game, id).Error
	if err != nil {
		return nil, err
	}
	return &game, nil
}

func (r *gameRepository) FindBySlug(slug string) (*domain.Game, error) {
	var game domain.Game
	err := r.db.Preload("Nominals", func(db *gorm.DB) *gorm.DB {
		return db.Where("is_active = ?", true).Order("sort_order ASC, base_price ASC")
	}).Preload("Providers").Where("slug = ? AND is_active = ?", slug, true).First(&game).Error
	if err != nil {
		return nil, err
	}
	return &game, nil
}

func (r *gameRepository) Update(game *domain.Game) error {
    updates := map[string]interface{}{
        "name":                game.Name,
        "slug":                game.Slug,
        "category":            game.Category,
        "publisher":           game.Publisher,
        "description":         game.Description,
        "image_url":           game.ImageURL,
        "banner_url":          game.BannerURL,
        "is_active":           game.IsActive,
        "is_popular":          game.IsPopular,
        "sort_order":          game.SortOrder,
        "has_zone_id":         game.HasZoneID,
        "user_id_label":       game.UserIDLabel,
        "zone_id_label":       game.ZoneIDLabel,
        "nickname_check_code": game.NicknameCheckCode,
    }

    return r.db.
        Model(&domain.Game{}).
        Where("id = ?", game.ID).
        Updates(updates).
        Error
}

func (r *gameRepository) Delete(id uint) error {
	return r.db.Delete(&domain.Game{}, id).Error
}

func (r *gameRepository) ListPublic() ([]domain.Game, error) {
	var games []domain.Game
	err := r.db.Where("is_active = ?", true).
		Order("is_popular DESC, sort_order ASC, name ASC").
		Find(&games).Error
	return games, err
}

func (r *gameRepository) ListAdmin(offset, limit int, search, category string) ([]domain.Game, int64, error) {
	var games []domain.Game
	var total int64

	query := r.db.Model(&domain.Game{})
	if search != "" {
		query = query.Where("name LIKE ? OR slug LIKE ?", "%"+search+"%", "%"+search+"%")
	}
	if category != "" {
		query = query.Where("category = ?", category)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Preload("Nominals").Preload("Providers").
		Order("sort_order ASC, id DESC").
		Offset(offset).Limit(limit).
		Find(&games).Error
	return games, total, err
}

func (r *gameRepository) SaveProviderMapping(mapping *domain.GameProvider) error {
	return r.db.Save(mapping).Error
}

func (r *gameRepository) GetProviderMappings(gameID uint) ([]domain.GameProvider, error) {
	var mappings []domain.GameProvider
	err := r.db.Where("game_id = ?", gameID).Find(&mappings).Error
	return mappings, err
}
