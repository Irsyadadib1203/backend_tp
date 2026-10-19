package repository

import (
	"gorm.io/gorm"
	"strings"

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
	ListAllAdmin(offset, limit int, gameID uint, providerID uint, search string, isActive, sortBy, sortDir string) ([]domain.Nominal, int64, error)
	ListForSellerH2H() ([]domain.Nominal, error)
	UpsertFromDigiflazz(nominals []domain.Nominal) error
	BatchSwitchProvider(nominalIDs []uint, providerID uint) error
	SwitchProviderByGame(gameID uint, providerID uint) error
	SwitchProviderWithPrices(providerID uint, updates []NominalSwitchUpdate) error
}

type nominalRepository struct {
	db *gorm.DB
}

type NominalSwitchUpdate struct {
	ID            uint
	ProviderProductCode string
	BasePrice     float64
	PricePublic   float64
	PriceMember   float64
	PriceVIP      float64
	PriceReseller float64
}

func NewNominalRepository(db *gorm.DB) NominalRepository {
	return &nominalRepository{db: db}
}

func (r *nominalRepository) Create(nominal *domain.Nominal) error {
	return r.db.Create(nominal).Error
}

func (r *nominalRepository) FindByID(id uint) (*domain.Nominal, error) {
	var nominal domain.Nominal
	err := r.db.Preload("Game").Preload("Provider").Preload("ProviderProducts.Provider").First(&nominal, id).Error
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
	code = strings.TrimSpace(code)
	var nominal domain.Nominal
	err := r.db.Preload("Game").Preload("Provider").Preload("ProviderProducts").
		Where("seller_product_code = ? AND is_active = ?", code, true).
		First(&nominal).Error
	if err != nil {
		return nil, err
	}
	return &nominal, nil
}

func (r *nominalRepository) Update(nominal *domain.Nominal) error {
	var existing domain.Nominal
	if err := r.db.First(&existing, nominal.ID).Error; err != nil {
		return err
	}

	if nominal.Name != "" {
		existing.Name = nominal.Name
	}
	existing.Description = nominal.Description
	if nominal.ProviderProductCode != "" {
		existing.ProviderProductCode = nominal.ProviderProductCode
	}
	if nominal.SellerProductCode != "" {
		existing.SellerProductCode = nominal.SellerProductCode
	}
	existing.KiosgamerProductCode = nominal.KiosgamerProductCode
	if nominal.GameID > 0 {
		existing.GameID = nominal.GameID
	}
	if nominal.ProviderID > 0 {
		existing.ProviderID = nominal.ProviderID
	}
	if nominal.BasePrice > 0 {
		existing.BasePrice = nominal.BasePrice
	}
	existing.PricePublic = nominal.PricePublic
	existing.PriceMember = nominal.PriceMember
	existing.PriceVIP = nominal.PriceVIP
	existing.PriceReseller = nominal.PriceReseller
	existing.MarginPercent = nominal.MarginPercent
	existing.MarginFlat = nominal.MarginFlat
	existing.IsActive = nominal.IsActive
	existing.SortOrder = nominal.SortOrder

	return r.db.Save(&existing).Error
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

func (r *nominalRepository) ListAllAdmin(offset, limit int, gameID uint, providerID uint, search string, isActive, sortBy, sortDir string) ([]domain.Nominal, int64, error) {
	var nominals []domain.Nominal
	var total int64

	query := r.db.Model(&domain.Nominal{})
	if gameID > 0 {
		query = query.Where("game_id = ?", gameID)
	}
	if providerID > 0 {
		query = query.Where("provider_id = ?", providerID)
	}
	if search != "" {
		query = query.Where("name LIKE ? OR provider_product_code LIKE ? OR seller_product_code LIKE ? OR kiosgamer_product_code LIKE ?", "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}
	// Filter status harus sebelum Count() supaya total ikut terfilter
	switch isActive {
	case "true":
		query = query.Where("is_active = ?", true)
	case "false":
		query = query.Where("is_active = ?", false)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// sortBy/sortDir sudah di-whitelist di handler; dicek lagi di sini agar aman
	dir := "ASC"
	if strings.EqualFold(sortDir, "desc") {
		dir = "DESC"
	}
	switch sortBy {
	case "name":
		query = query.Order("name " + dir).Order("id ASC")
	case "base_price":
		query = query.Order("base_price " + dir).Order("id ASC")
	default:
		query = query.Order("game_id ASC, sort_order ASC, id DESC")
	}

	err := query.Preload("Game").Preload("Provider").Preload("ProviderProducts.Provider").
		Offset(offset).Limit(limit).
		Find(&nominals).Error
	return nominals, total, err
}

func (r *nominalRepository) BatchSwitchProvider(nominalIDs []uint, providerID uint) error {
	if len(nominalIDs) == 0 || providerID == 0 {
		return nil
	}
	return r.db.Model(&domain.Nominal{}).
		Where("id IN ?", nominalIDs).
		Update("provider_id", providerID).Error
}

func (r *nominalRepository) SwitchProviderByGame(gameID uint, providerID uint) error {
	if gameID == 0 || providerID == 0 {
		return nil
	}
	return r.db.Model(&domain.Nominal{}).
		Where("game_id = ?", gameID).
		Update("provider_id", providerID).Error
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
				// Nominal baru
				nom.CalculatePrices()
				if err := tx.Create(&nom).Error; err != nil {
					return err
				}
				continue
			}

			if nom.Name != "" {
				existing.Name = nom.Name
			}
			// Harga & status hanya mengikuti Digiflazz jika Digiflazz
			// memang provider aktif nominal ini.
			if existing.ProviderID == nom.ProviderID {
				existing.BasePrice = nom.BasePrice
				existing.IsActive = nom.IsActive
				existing.ForceCalculatePrices()
			}
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *nominalRepository) SwitchProviderWithPrices(providerID uint, updates []NominalSwitchUpdate) error {
	if providerID == 0 || len(updates) == 0 {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, u := range updates {
			if err := tx.Model(&domain.Nominal{}).Where("id = ?", u.ID).Updates(map[string]interface{}{
				"provider_id":    providerID,
				"provider_product_code": u.ProviderProductCode,
				"base_price":     u.BasePrice,
				"price_public":   u.PricePublic,
				"price_member":   u.PriceMember,
				"price_vip":      u.PriceVIP,
				"price_reseller": u.PriceReseller,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
