package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/cache"
	"topup-backend/internal/pkg/utils"
	"topup-backend/internal/provider"
	"topup-backend/internal/provider/digiflazz"
	"topup-backend/internal/repository"
)

type SwitchProviderResult struct {
	TotalRequested int      `json:"total_requested"`
	SwitchedCount  int      `json:"switched_count"`
	SkippedCount   int      `json:"skipped_count"`
	SkippedItems   []string `json:"skipped_items"`
	Message        string   `json:"message"`
}

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
	ListNominalsAdmin(offset, limit int, gameID uint, providerID uint, search string, isActive, sortBy, sortDir string) ([]domain.Nominal, int64, error)
	BatchSwitchProvider(nominalIDs []uint, providerID uint) (*SwitchProviderResult, error)
	SwitchProviderByGame(gameID uint, providerID uint) (*SwitchProviderResult, error)
	SyncBasePriceFromActiveProvider(nominalID uint) (bool, error)

	// Catalog Sync from Digiflazz
	SyncDigiflazzProducts(targetGameID uint, brandFilter string, defaultMarginPercent float64) (int, error)
	AutoSyncAllPrices() (int, error)
}

// DigiflazzCatalog adalah bagian klien Digiflazz yang dipakai untuk sinkronisasi katalog/harga.
// Diimplementasikan oleh digiflazz.Client.
type DigiflazzCatalog interface {
	GetPriceList() ([]digiflazz.PriceListItem, error)
}

type gameService struct {
	gameRepo            repository.GameRepository
	nominalRepo         repository.NominalRepository
	providerRepo        repository.ProviderRepository
	digiflazzBuyer      DigiflazzCatalog
	providerProductRepo repository.ProviderProductRepository
	providerRegistry    *provider.Registry
}

func NewGameService(
	gameRepo repository.GameRepository,
	nominalRepo repository.NominalRepository,
	providerRepo repository.ProviderRepository,
	digiflazzBuyer DigiflazzCatalog,
	extra ...interface{},
) GameService {
	s := &gameService{
		gameRepo:       gameRepo,
		nominalRepo:    nominalRepo,
		providerRepo:   providerRepo,
		digiflazzBuyer: digiflazzBuyer,
	}
	for _, opt := range extra {
		switch v := opt.(type) {
		case repository.ProviderProductRepository:
			s.providerProductRepo = v
		case *provider.Registry:
			s.providerRegistry = v
		}
	}
	return s
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

func (s *gameService) isGameSupportedByProvider(p *domain.Provider, game *domain.Game) bool {
	if p == nil || game == nil {
		return false
	}
	// Capability GameSupport via registry (§5 Fase 3)
	if s.providerRegistry != nil {
		if adapter, ok := s.providerRegistry.Get(p.Code); ok {
			if gs, ok := adapter.(provider.GameSupport); ok {
				return gs.Supports(game.Slug) || (game.Name != "" && gs.Supports(game.Name))
			}
			// Providers without GameSupport capability support all games by default
			return true
		}
	}
	// Fallback to legacy check for Kiosgamer
	if strings.EqualFold(p.Code, "KIOSGAMER") {
		return isKiosgamerSupportedGame(game)
	}
	return true
}

func (s *gameService) validateNominalProvider(nominal *domain.Nominal) error {
	if nominal.ProviderID == 0 {
		return nil
	}
	provider, err := s.providerRepo.GetByID(nominal.ProviderID)
	if err != nil || provider == nil {
		return nil
	}

	game, err := s.gameRepo.FindByID(nominal.GameID)
	if err != nil || game == nil {
		if strings.EqualFold(provider.Code, "KIOSGAMER") {
			return errors.New("game target tidak ditemukan")
		}
		return nil
	}

	if !s.isGameSupportedByProvider(provider, game) {
		if strings.EqualFold(provider.Code, "KIOSGAMER") {
			return fmt.Errorf("provider Kiosgamer hanya mendukung game Free Fire & Call of Duty Mobile. Game '%s' tidak didukung", game.Name)
		}
		return fmt.Errorf("provider %s tidak mendukung game '%s'", provider.Name, game.Name)
	}
	if strings.EqualFold(provider.Code, "KIOSGAMER") &&
		strings.TrimSpace(nominal.ProviderProductCode) == "" &&
		strings.TrimSpace(nominal.KiosgamerProductCode) == "" {
		return errors.New("SKU Kiosgamer wajib diisi jika memilih provider pemrosesan Kiosgamer")
	}
	return nil
}

func (s *gameService) CreateNominal(nominal *domain.Nominal) error {
	if err := s.validateNominalProvider(nominal); err != nil {
		return err
	}
	if nominal.SellerProductCode == "" {
		nominal.SellerProductCode = nominal.ProviderProductCode
	}
	nominal.CalculatePrices()
	err := s.nominalRepo.Create(nominal)
	if err == nil {
		s.dualWriteProviderProducts(nominal, nil)
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) UpdateNominal(nominal *domain.Nominal) error {
	if err := s.validateNominalProvider(nominal); err != nil {
		return err
	}
	// Dual-write safety: kolom lama tetap ditulis dan tidak boleh tertimpa kosong (§3.6, §5)
	existing, _ := s.nominalRepo.FindByID(nominal.ID)
	if existing != nil {
		if nominal.ProviderID == 0 {
			nominal.ProviderID = existing.ProviderID
		}
		if existing.ProviderID != 0 && nominal.ProviderID != existing.ProviderID {
			return errors.New("provider pemrosesan tidak bisa diubah lewat edit; gunakan fitur Pindah Provider")
		}
		if strings.TrimSpace(nominal.ProviderProductCode) == "" && existing.ProviderProductCode != "" {
			nominal.ProviderProductCode = existing.ProviderProductCode
		}
		if strings.TrimSpace(nominal.KiosgamerProductCode) == "" && existing.KiosgamerProductCode != "" {
			nominal.KiosgamerProductCode = existing.KiosgamerProductCode
		}
		if strings.TrimSpace(nominal.SellerProductCode) == "" && existing.SellerProductCode != "" {
			nominal.SellerProductCode = existing.SellerProductCode
		}
	}
	nominal.CalculatePrices()
	err := s.nominalRepo.Update(nominal)
	if err == nil {
		s.dualWriteProviderProducts(nominal, existing)
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) dualWriteProviderProducts(nominal, existing *domain.Nominal) {
	if s.providerProductRepo == nil || nominal == nil || nominal.ID == 0 {
		return
	}
	code := strings.TrimSpace(nominal.ProviderProductCode)
	if code == "" {
		return
	}
	pID := nominal.ProviderID
	if pID == 0 {
		if p, err := s.providerRepo.GetByCode("DIGIFLAZZ"); err == nil && p != nil {
			pID = p.ID
		}
	}
	if pID == 0 {
		return
	}

	pp := &domain.ProviderProduct{
		NominalID:     nominal.ID,
		ProviderID:    pID,
		ProductCode:   code,
		IsActive:      nominal.IsActive,
		AutoSyncPrice: true,
	}
	var prev *domain.ProviderProduct
	if existing != nil {
		for i := range existing.ProviderProducts {
			if existing.ProviderProducts[i].ProviderID == pID {
				prev = &existing.ProviderProducts[i]
				break
			}
		}
	}
	if prev != nil {
		pp.CostPrice = prev.CostPrice
		pp.AutoSyncPrice = prev.AutoSyncPrice
	} else {
		cp := nominal.BasePrice
		pp.CostPrice = &cp
	}
	_ = s.providerProductRepo.Upsert(pp)
}

func (s *gameService) DeleteNominal(id uint) error {
	_ = s.providerProductRepo.DeleteByNominalID(id)
	err := s.nominalRepo.Delete(id)
	if err == nil {
		s.invalidateGameCache()
	}
	return err
}

func (s *gameService) ListNominalsAdmin(offset, limit int, gameID uint, providerID uint, search string, isActive, sortBy, sortDir string) ([]domain.Nominal, int64, error) {
	return s.nominalRepo.ListAllAdmin(offset, limit, gameID, providerID, search, isActive, sortBy, sortDir)
}

func isKiosgamerSupportedGame(game *domain.Game) bool {
	if game == nil {
		return false
	}
	slug := strings.ToLower(strings.TrimSpace(game.Slug))
	name := strings.ToLower(strings.TrimSpace(game.Name))

	if strings.Contains(slug, "free-fire") || strings.Contains(slug, "freefire") || strings.Contains(name, "free fire") {
		return true
	}
	if strings.Contains(slug, "codm") || strings.Contains(slug, "call-of-duty") || strings.Contains(name, "call of duty") || strings.Contains(name, "codm") {
		return true
	}
	return false
}

func (s *gameService) BatchSwitchProvider(nominalIDs []uint, providerID uint) (*SwitchProviderResult, error) {
	provider, err := s.providerRepo.GetByID(providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve provider: %w", err)
	}
	if provider == nil {
		return nil, errors.New("provider not found")
	}

	result := &SwitchProviderResult{
		TotalRequested: len(nominalIDs),
		SkippedItems:   make([]string, 0),
	}
	skip := func(msg string) {
		result.SkippedCount++
		result.SkippedItems = append(result.SkippedItems, msg)
	}

	isKios := strings.EqualFold(provider.Code, "KIOSGAMER")
	var updates []repository.NominalSwitchUpdate

	for _, nid := range nominalIDs {
		nom, err := s.nominalRepo.FindByID(nid)
		if err != nil || nom == nil {
			skip(fmt.Sprintf("Nominal ID %d (Tidak ditemukan)", nid))
			continue
		}

		// 1. Game harus didukung provider tujuan
		game := nom.Game
		if game == nil && nom.GameID > 0 {
			game, _ = s.gameRepo.FindByID(nom.GameID)
		}
		if !s.isGameSupportedByProvider(provider, game) {
			gameTitle := "Game ini"
			if game != nil {
				gameTitle = game.Name
			}
			skip(fmt.Sprintf("%s (%s tidak didukung oleh %s, tetap di provider saat ini)", nom.Name, gameTitle, provider.Name))
			continue
		}

		// 2. Cari provider_product milik provider tujuan
		var pp *domain.ProviderProduct
		for i := range nom.ProviderProducts {
			if nom.ProviderProducts[i].ProviderID == providerID {
				pp = &nom.ProviderProducts[i]
				break
			}
		}

		// 3. SKU provider tujuan harus ada
		sku := ""
		if pp != nil {
			sku = strings.TrimSpace(pp.ProductCode)
		}
		if sku == "" && isKios {
			sku = strings.TrimSpace(nom.KiosgamerProductCode) // legacy
		}
		if sku == "" {
			skip(fmt.Sprintf("%s (SKU %s belum diisi, tetap di provider saat ini)", nom.Name, provider.Name))
			continue
		}

		// 4. Harga modal provider tujuan harus ada dan > 0
		if pp == nil || pp.CostPrice == nil || *pp.CostPrice <= 0 {
			skip(fmt.Sprintf("%s (Harga modal %s belum diisi, tetap di provider saat ini)", nom.Name, provider.Name))
			continue
		}

		// 5. base_price = cost_price, semua tier dihitung ulang dari margin nominal ini
		
		nom.ApplyBasePrice(*pp.CostPrice)
		updates = append(updates, repository.NominalSwitchUpdate{
			ID:                  nom.ID,
			ProviderProductCode: sku,
			BasePrice:           nom.BasePrice,
			PricePublic:         nom.PricePublic,
			PriceMember:         nom.PriceMember,
			PriceVIP:            nom.PriceVIP,
			PriceReseller:       nom.PriceReseller,
		})
	}

	if len(updates) > 0 {
		if err := s.nominalRepo.SwitchProviderWithPrices(providerID, updates); err != nil {
			return nil, fmt.Errorf("gagal memperbarui provider: %w", err)
		}
		s.invalidateGameCache()
		result.SwitchedCount = len(updates)
	}

	switch {
	case result.SkippedCount > 0 && result.SwitchedCount > 0:
		result.Message = fmt.Sprintf("%d nominal berhasil dialihkan ke %s (harga modal & harga jual ikut diperbarui). %d nominal tetap di provider lama (game tidak didukung / SKU atau harga modal belum diisi).", result.SwitchedCount, provider.Name, result.SkippedCount)
	case result.SkippedCount > 0:
		result.Message = fmt.Sprintf("Tidak ada nominal yang dialihkan. Semua %d nominal tetap di provider lama karena game tidak didukung atau SKU/harga modal belum diisi.", result.SkippedCount)
	default:
		result.Message = fmt.Sprintf("Berhasil mengalihkan %d nominal ke provider %s. Harga modal dan harga jual sudah diperbarui.", result.SwitchedCount, provider.Name)
	}
	return result, nil
}

func (s *gameService) SwitchProviderByGame(gameID uint, providerID uint) (*SwitchProviderResult, error) {
	provider, err := s.providerRepo.GetByID(providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve provider: %w", err)
	}
	if provider == nil {
		return nil, errors.New("provider not found")
	}

	nominals, _, err := s.nominalRepo.ListAllAdmin(0, 0, gameID, 0, "", "", "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to list nominals for game %d: %w", gameID, err)
	}

	var nominalIDs []uint
	for _, n := range nominals {
		nominalIDs = append(nominalIDs, n.ID)
	}

	if len(nominalIDs) == 0 {
		return &SwitchProviderResult{
			TotalRequested: 0,
			SwitchedCount:  0,
			SkippedCount:   0,
			SkippedItems:   []string{},
			Message:        "Tidak ada produk nominal pada game ini.",
		}, nil
	}

	return s.BatchSwitchProvider(nominalIDs, providerID)
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
		if s.providerProductRepo != nil {
			for _, nom := range syncedNominals {
				if existing, err := s.nominalRepo.FindByProviderCode(nom.ProviderProductCode); err == nil && existing != nil {
					costPrice := nom.BasePrice
					_ = s.providerProductRepo.Upsert(&domain.ProviderProduct{
						NominalID:   existing.ID,
						ProviderID:  providerID,
						ProductCode: existing.ProviderProductCode,
						CostPrice:   &costPrice,
						IsActive:    existing.IsActive,
					})
				}
			}
		}
	}

	return len(syncedNominals), nil
}

func (s *gameService) AutoSyncAllPrices() (int, error) {
	// 1. Ambil harga terbaru dari Digiflazz
	products, err := s.digiflazzBuyer.GetPriceList()
	if err != nil {
		return 0, err
	}

	// 2. Map produk Digiflazz berdasarkan SKU
	skuMap := make(map[string]digiflazz.PriceListItem, len(products))
	for _, p := range products {
		skuMap[strings.TrimSpace(p.BuyerSkuCode)] = p
	}

	// 3. Ambil semua nominal dari database
	existingNominals, _, err := s.nominalRepo.ListAllAdmin(0, 5000, 0, 0, "", "", "", "")
	if err != nil {
		return 0, err
	}

	digiProvider, _ := s.providerRepo.GetByCode("DIGIFLAZZ")
	var digiID uint
	if digiProvider != nil {
		digiID = digiProvider.ID
	}

	updatedCount := 0
	for _, nom := range existingNominals {
		digiSku := ""
		for _, pp := range nom.ProviderProducts {
			if pp.ProviderID == digiID {
				digiSku = strings.TrimSpace(pp.ProductCode)
				break
			}
		}
		if digiSku == "" && nom.ProviderID == digiID {
			digiSku = strings.TrimSpace(nom.ProviderProductCode)
		}
		digiItem, found := skuMap[digiSku]
		if !found {
			continue
		}
		digiActive := digiItem.BuyerProductStatus && digiItem.SellerProductStatus
		autoSync := true
		for _, pp := range nom.ProviderProducts {
			if pp.ProviderID == digiID {
				autoSync = pp.AutoSyncPrice
				break
			}
		}
		if !autoSync {
			continue
		}
		if s.providerProductRepo != nil && digiID > 0 {
			cp := digiItem.Price
			_ = s.providerProductRepo.Upsert(&domain.ProviderProduct{
				NominalID:     nom.ID,
				ProviderID:    digiID,
				ProductCode:   digiSku,
				CostPrice:     &cp,
				IsActive:      digiActive,
				AutoSyncPrice: true,
			})
		}

		// Harga jual & status nominal hanya mengikuti Digiflazz
		// jika Digiflazz adalah provider aktif nominal ini.
		if digiID > 0 && nom.ProviderID != digiID {
			continue
		}

		priceChanged := nom.BasePrice != digiItem.Price
		statusChanged := nom.IsActive != digiActive
		if priceChanged || statusChanged {
			nom.BasePrice = digiItem.Price
			nom.IsActive = digiActive
			nom.ForceCalculatePrices()
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
func (s *gameService) SyncBasePriceFromActiveProvider(nominalID uint) (bool, error) {
	nom, err := s.nominalRepo.FindByID(nominalID)
	if err != nil || nom == nil {
		return false, fmt.Errorf("nominal %d tidak ditemukan", nominalID)
	}
	for i := range nom.ProviderProducts {
		pp := nom.ProviderProducts[i]
		if pp.ProviderID != nom.ProviderID {
			continue
		}
		if pp.CostPrice == nil || *pp.CostPrice <= 0 {
			return false, nil
		}
		if nom.BasePrice == *pp.CostPrice {
			return false, nil
		}
		nom.ApplyBasePrice(*pp.CostPrice)
		if err := s.nominalRepo.Update(nom); err != nil {
			return false, err
		}
		s.invalidateGameCache()
		return true, nil
	}
	return false, nil
}
