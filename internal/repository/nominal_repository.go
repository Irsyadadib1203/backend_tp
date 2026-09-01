package repository

import (
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type NominalRepository interface {
	Create(nominal *domain.Nominal) error
	FindByID(id uint) (*domain.Nominal, error)
	FindByProviderCode(code string) (*domain.Nominal, error)
	FindBySellerCode(code string) (*domain.Nominal, error)
	Update(nominal *domain.Nominal) error
	Delete(id uint) error
	ListByGameID(gameID uint) ([]domain.Nominal, error)
	ListAllAdmin(offset, limit int, gameID uint, search string) ([]domain.Nominal, int64, error)
	ListForSellerH2H() ([]domain.Nominal, error)
	UpsertFromDigiflazz(nominals []domain.Nominal) error
}

type nominalRepository struct {
	db *gorm.DB
}

func NewNominalRepository(db *gorm.DB) NominalRepository {
	return &nominalRepository{db: db}
}

func (r *nominalRepository) Create(nominal *domain.Nominal) error {
	return r.db.Create(nominal).Error
}

func (r *nominalRepository) FindByID(id uint) (*domain.Nominal, error) {
	var nominal domain.Nominal
	err := r.db.Preload("Game").Preload("Provider").First(&nominal, id).Error
	if err != nil {
		return nil, err
	}
	return &nominal, nil
}

func (r *nominalRepository) FindByProviderCode(code string) (*domain.Nominal, error) {
	var nominal domain.Nominal
	err := r.db.Preload("Game").Where("provider_product_code = ?", code).First(&nominal).Error
	if err != nil {
		return nil, err
	}
	return &nominal, nil
}

func (r *nominalRepository) FindBySellerCode(code string) (*domain.Nominal, error) {
	var nominal domain.Nominal
	err := r.db.Preload("Game").Preload("Provider").
		Where("(seller_product_code = ? OR provider_product_code = ?) AND is_active = ?", code, code, true).
		First(&nominal).Error
	if err != nil {
		return nil, err
	}
	return &nominal, nil
}

func (r *nominalRepository) Update(nominal *domain.Nominal) error {
    updates := map[string]interface{}{
        "name":                  nominal.Name,
        "provider_product_code": nominal.ProviderProductCode,
        "seller_product_code":   nominal.SellerProductCode,
        "base_price":            nominal.BasePrice,
        "price_public":          nominal.PricePublic,
        "price_reseller":        nominal.PriceReseller,
        "is_active":             nominal.IsActive,
        "sort_order":            nominal.SortOrder,
    }

    if nominal.GameID > 0 {
        updates["game_id"] = nominal.GameID
    }

    if nominal.ProviderID > 0 {
        updates["provider_id"] = nominal.ProviderID
    }

    return r.db.
        Model(&domain.Nominal{}).
        Where("id = ?", nominal.ID).
        Updates(updates).
        Error
}

func (r *nominalRepository) Delete(id uint) error {
	return r.db.Delete(&domain.Nominal{}, id).Error
}

func (r *nominalRepository) ListByGameID(gameID uint) ([]domain.Nominal, error) {
	var nominals []domain.Nominal
	err := r.db.Where("game_id = ? AND is_active = ?", gameID, true).
		Order("sort_order ASC, base_price ASC").
		Find(&nominals).Error
	return nominals, err
}

func (r *nominalRepository) ListAllAdmin(offset, limit int, gameID uint, search string) ([]domain.Nominal, int64, error) {
	var nominals []domain.Nominal
	var total int64

	query := r.db.Model(&domain.Nominal{})
	if gameID > 0 {
		query = query.Where("game_id = ?", gameID)
	}
	if search != "" {
		query = query.Where("name LIKE ? OR provider_product_code LIKE ? OR seller_product_code LIKE ?", "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Preload("Game").Preload("Provider").
		Order("game_id ASC, sort_order ASC, id DESC").
		Offset(offset).Limit(limit).
		Find(&nominals).Error
	return nominals, total, err
}

func (r *nominalRepository) ListForSellerH2H() ([]domain.Nominal, error) {
	var nominals []domain.Nominal
	err := r.db.Preload("Game").
		Where("is_active = ?", true).
		Order("game_id ASC, price_reseller ASC").
		Find(&nominals).Error
	return nominals, err
}

func (r *nominalRepository) UpsertFromDigiflazz(nominals []domain.Nominal) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, nom := range nominals {
			var existing domain.Nominal
			err := tx.Where("provider_product_code = ?", nom.ProviderProductCode).First(&existing).Error
			if err != nil {
				// Create new
				nom.CalculatePrices()
				if err := tx.Create(&nom).Error; err != nil {
					return err
				}
			} else {
				// Update base price and recalculate
				existing.BasePrice = nom.BasePrice
				existing.IsActive = nom.IsActive
				if nom.Name != "" {
					existing.Name = nom.Name
				}
				existing.CalculatePrices()
				if err := tx.Save(&existing).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
