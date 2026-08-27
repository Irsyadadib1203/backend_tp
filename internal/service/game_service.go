package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/cache"
	"topup-backend/internal/pkg/utils"
	"topup-backend/internal/repository"
)

type GameService interface {
	GetPublicGames() ([]domain.Game, error)
	GetGameBySlug(slug string) (*domain.Game, error)
	GetGameByID(id uint) (*domain.Game, error)
	CreateGame(game *domain.Game) error
	UpdateGame(game *domain.Game) error
	DeleteGame(id uint) error
	ListGamesAdmin(offset, limit int, search, category string) ([]domain.Game, int64, error)
	
	// Nominals
	GetNominalByID(id uint) (*domain.Nominal, error)
	CreateNominal(nominal *domain.Nominal) error
	UpdateNominal(nominal *domain.Nominal) error
	DeleteNominal(id uint) error
	ListNominalsAdmin(offset, limit int, gameID uint, search string) ([]domain.Nominal, int64, error)
	
	// Catalog Sync from Digiflazz
	SyncDigiflazzProducts(targetGameID uint, brandFilter string, defaultMarginPercent float64) (int, error)
	AutoSyncAllPrices() (int, error)
}

type gameService struct {
	gameRepo       repository.GameRepository
	nominalRepo    repository.NominalRepository
	providerRepo   repository.ProviderRepository
	digiflazzBuyer DigiflazzBuyerService
}

func NewGameService(
	gameRepo repository.GameRepository,
	nominalRepo repository.NominalRepository,
	providerRepo repository.ProviderRepository,
	digiflazzBuyer DigiflazzBuyerService,
) GameService {
	return &gameService{
		gameRepo:       gameRepo,
		nominalRepo:    nominalRepo,
		providerRepo:   providerRepo,
		digiflazzBuyer: digiflazzBuyer,
	}
}

func (s *gameService) GetPublicGames() ([]domain.Game, error) {
	cacheKey := "games:public:all"
	if cached, ok := cache.GlobalCache.Get(cacheKey); ok {
		if games, valid := cached.([]domain.Game); valid {
			return games, nil
		}
	}

	games, err := s.gameRepo.ListPublic()
	if err == nil && len(games) > 0 {
		cache.GlobalCache.Set(cacheKey, games, 5*time.Minute)
	}
	return games, err
}

func (s *gameService) GetGameBySlug(slug string) (*domain.Game, error) {
	cacheKey := fmt.Sprintf("game:slug:%s", slug)
	if cached, ok := cache.GlobalCache.Get(cacheKey); ok {
		if game, valid := cached.(*domain.Game); valid {
			return game, nil
		}
	}

	game, err := s.gameRepo.FindBySlug(slug)
	if err == nil && game != nil {
		cache.GlobalCache.Set(cacheKey, game, 5*time.Minute)
	}
	return game, err
}

func (s *gameService) GetGameByID(id uint) (*domain.Game, error) {
	cacheKey := fmt.Sprintf("game:id:%d", id)
	if cached, ok := cache.GlobalCache.Get(cacheKey); ok {
		if game, valid := cached.(*domain.Game); valid {
			return game, nil
		}
	}

	game, err := s.gameRepo.FindByID(id)
	if err == nil && game != nil {
		cache.GlobalCache.Set(cacheKey, game, 5*time.Minute)
	}
	return game, err
}

func (s *gameService) CreateGame(game *domain.Game) error {
	if game.Slug == "" {
		game.Slug = utils.Slugify(game.Name)
	}
	err := s.gameRepo.Create(game)
	if err == nil {
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) UpdateGame(game *domain.Game) error {
	err := s.gameRepo.Update(game)
	if err == nil {
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) DeleteGame(id uint) error {
	err := s.gameRepo.Delete(id)
	if err == nil {
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) invalidateGameCache() {
	cache.GlobalCache.Delete("games:public:all")
	cache.GlobalCache.DeletePrefix("game:slug:")
	cache.GlobalCache.DeletePrefix("game:id:")
}

func (s *gameService) ListGamesAdmin(offset, limit int, search, category string) ([]domain.Game, int64, error) {
	return s.gameRepo.ListAdmin(offset, limit, search, category)
}

func (s *gameService) GetNominalByID(id uint) (*domain.Nominal, error) {
	return s.nominalRepo.FindByID(id)
}

func (s *gameService) CreateNominal(nominal *domain.Nominal) error {
	if nominal.SellerProductCode == "" {
		nominal.SellerProductCode = nominal.ProviderProductCode
	}
	nominal.CalculatePrices()
	err := s.nominalRepo.Create(nominal)
	if err == nil {
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) UpdateNominal(nominal *domain.Nominal) error {
	nominal.CalculatePrices()
	err := s.nominalRepo.Update(nominal)
	if err == nil {
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) DeleteNominal(id uint) error {
	err := s.nominalRepo.Delete(id)
	if err == nil {
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) ListNominalsAdmin(offset, limit int, gameID uint, search string) ([]domain.Nominal, int64, error) {
	return s.nominalRepo.ListAllAdmin(offset, limit, gameID, search)
}

func (s *gameService) SyncDigiflazzProducts(targetGameID uint, brandFilter string, defaultMarginPercent float64) (int, error) {
	products, err := s.digiflazzBuyer.GetPriceList()
	if err != nil {
		return 0, err
	}

	provider, _ := s.providerRepo.GetByCode("DIGIFLAZZ")
	providerID := uint(1)
	if provider != nil {
		providerID = provider.ID
	}

	game, err := s.gameRepo.FindByID(targetGameID)
	if err != nil || game == nil {
		return 0, errors.New("game target not found")
	}

	if defaultMarginPercent <= 0 {
		defaultMarginPercent = 8.0 // default 8% markup
	}

	var syncedNominals []domain.Nominal
	for _, item := range products {
		if brandFilter != "" && !strings.EqualFold(item.Brand, brandFilter) {
			continue
		}

		// Filter category Games or matching brand
		if strings.EqualFold(item.Brand, game.Name) || (brandFilter != "" && strings.EqualFold(item.Brand, brandFilter)) {
			nom := domain.Nominal{
				GameID:              game.ID,
				ProviderID:          providerID,
				Name:                item.ProductName,
				Description:         item.Desc,
				BasePrice:           item.Price,
				MarginPercent:       defaultMarginPercent,
				ProviderProductCode: item.BuyerSkuCode,
				SellerProductCode:   fmt.Sprintf("%s_%s", utils.Slugify(game.Slug), item.BuyerSkuCode),
				IsActive:            item.BuyerProductStatus && item.SellerProductStatus,
			}
			nom.CalculatePrices()
			syncedNominals = append(syncedNominals, nom)
		}
	}

	if len(syncedNominals) > 0 {
		if err := s.nominalRepo.UpsertFromDigiflazz(syncedNominals); err != nil {
			return 0, err
		}
		s.invalidateGameCache()
	}

	return len(syncedNominals), nil
}

func (s *gameService) AutoSyncAllPrices() (int, error) {
	// 1. Fetch latest prices from Digiflazz
	products, err := s.digiflazzBuyer.GetPriceList()
	if err != nil {
		return 0, err
	}

	// 2. Map Digiflazz products by SKU for O(1) lookup
	skuMap := make(map[string]DigiflazzPriceListItem, len(products))
	for _, p := range products {
		skuMap[strings.TrimSpace(p.BuyerSkuCode)] = p
	}

	// 3. Fetch all nominals from database
	existingNominals, _, err := s.nominalRepo.ListAllAdmin(0, 5000, 0, "")
	if err != nil {
		return 0, err
	}

	updatedCount := 0
	for _, nom := range existingNominals {
		digiItem, found := skuMap[strings.TrimSpace(nom.ProviderProductCode)]
		if !found {
			continue
		}

		// Check if price or status changed
		priceChanged := nom.BasePrice != digiItem.Price
		statusChanged := nom.IsActive != (digiItem.BuyerProductStatus && digiItem.SellerProductStatus)

		if priceChanged || statusChanged {
			nom.BasePrice = digiItem.Price
			nom.IsActive = digiItem.BuyerProductStatus && digiItem.SellerProductStatus
			nom.CalculatePrices()
			if err := s.nominalRepo.Update(&nom); err == nil {
				updatedCount++
			}
		}
	}

	if updatedCount > 0 {
		s.invalidateGameCache()
	}

	return updatedCount, nil
}

