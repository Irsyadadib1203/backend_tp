package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
)

// Mock ProviderRepository
type mockProviderRepo struct {
	providers map[uint]*domain.Provider
}

func (m *mockProviderRepo) GetByCode(code string) (*domain.Provider, error) {
	for _, p := range m.providers {
		if p.Code == code {
			return p, nil
		}
	}
	return nil, nil
}

func (m *mockProviderRepo) GetByID(id uint) (*domain.Provider, error) {
	if p, ok := m.providers[id]; ok {
		return p, nil
	}
	return nil, nil
}

func (m *mockProviderRepo) List() ([]domain.Provider, error) {
	var list []domain.Provider
	for _, p := range m.providers {
		list = append(list, *p)
	}
	return list, nil
}

func (m *mockProviderRepo) Update(provider *domain.Provider) error {
	m.providers[provider.ID] = provider
	return nil
}

func (m *mockProviderRepo) UpdateBalance(id uint, balance float64) error {
	if p, ok := m.providers[id]; ok {
		p.Balance = balance
	}
	return nil
}
func (m *mockProviderRepo) UpdatePriceSyncAt(uint, time.Time) error { return nil }

func (m *mockProviderRepo) LogWebhook(log *domain.WebhookLog) error {
	return nil
}

func (m *mockProviderRepo) ListWebhookLogs(offset, limit int, provider string) ([]domain.WebhookLog, int64, error) {
	return nil, 0, nil
}

// Mock GameRepository
type mockGameRepo struct {
	games map[uint]*domain.Game
}

func (m *mockGameRepo) Create(game *domain.Game) error {
	m.games[game.ID] = game
	return nil
}

func (m *mockGameRepo) FindByID(id uint) (*domain.Game, error) {
	if g, ok := m.games[id]; ok {
		return g, nil
	}
	return nil, nil
}

func (m *mockGameRepo) FindBySlug(slug string) (*domain.Game, error) {
	for _, g := range m.games {
		if g.Slug == slug {
			return g, nil
		}
	}
	return nil, nil
}

func (m *mockGameRepo) Update(game *domain.Game) error {
	m.games[game.ID] = game
	return nil
}

func (m *mockGameRepo) Delete(id uint) error {
	delete(m.games, id)
	return nil
}

func (m *mockGameRepo) ListPublic() ([]domain.Game, error) {
	var list []domain.Game
	for _, g := range m.games {
		if g.IsActive {
			list = append(list, *g)
		}
	}
	return list, nil
}

func (m *mockGameRepo) ListAdmin(offset, limit int, search, category string) ([]domain.Game, int64, error) {
	var list []domain.Game
	for _, g := range m.games {
		list = append(list, *g)
	}
	return list, int64(len(list)), nil
}

func (m *mockGameRepo) SaveProviderMapping(mapping *domain.GameProvider) error {
	return nil
}

func (m *mockGameRepo) GetProviderMappings(gameID uint) ([]domain.GameProvider, error) {
	return nil, nil
}

// Mock NominalRepository
type mockNominalRepo struct {
	nominals map[uint]*domain.Nominal
}

func (m *mockNominalRepo) Create(nominal *domain.Nominal) error {
	m.nominals[nominal.ID] = nominal
	return nil
}

func (m *mockNominalRepo) FindByID(id uint) (*domain.Nominal, error) {
	if n, ok := m.nominals[id]; ok {
		return n, nil
	}
	return nil, nil
}

func (m *mockNominalRepo) FindByProviderCode(code string) (*domain.Nominal, error) {
	for _, n := range m.nominals {
		if n.ProviderProductCode == code {
			return n, nil
		}
	}
	return nil, nil
}

func (m *mockNominalRepo) FindBySellerCode(code string) (*domain.Nominal, error) {
	for _, n := range m.nominals {
		if n.SellerProductCode == code {
			return n, nil
		}
	}
	return nil, nil
}

func (m *mockNominalRepo) ListForSellerH2H() ([]domain.Nominal, error) {
	var list []domain.Nominal
	for _, n := range m.nominals {
		list = append(list, *n)
	}
	return list, nil
}

func (m *mockNominalRepo) Update(nominal *domain.Nominal) error {
	m.nominals[nominal.ID] = nominal
	return nil
}

func (m *mockNominalRepo) Delete(id uint) error {
	delete(m.nominals, id)
	return nil
}

func (m *mockNominalRepo) ListByGameID(gameID uint) ([]domain.Nominal, error) {
	var list []domain.Nominal
	for _, n := range m.nominals {
		if n.GameID == gameID {
			list = append(list, *n)
		}
	}
	return list, nil
}

func (m *mockNominalRepo) ListAllAdmin(offset, limit int, gameID uint, providerID uint, search string) ([]domain.Nominal, int64, error) {
	var list []domain.Nominal
	for _, n := range m.nominals {
		if gameID > 0 && n.GameID != gameID {
			continue
		}
		if providerID > 0 && n.ProviderID != providerID {
			continue
		}
		list = append(list, *n)
	}
	return list, int64(len(list)), nil
}

func (m *mockNominalRepo) BatchSwitchProvider(nominalIDs []uint, providerID uint) error {
	for _, id := range nominalIDs {
		if n, ok := m.nominals[id]; ok {
			n.ProviderID = providerID
		}
	}
	return nil
}

func (m *mockNominalRepo) SwitchProviderByGame(gameID uint, providerID uint) error {
	for _, n := range m.nominals {
		if n.GameID == gameID {
			n.ProviderID = providerID
		}
	}
	return nil
}

func (m *mockNominalRepo) UpsertFromDigiflazz(nominals []domain.Nominal) error {
	for i := range nominals {
		m.nominals[nominals[i].ID] = &nominals[i]
	}
	return nil
}

func TestBatchSwitchProvider_Kiosgamer_GameWhitelistAndSKUValidation(t *testing.T) {
	providerRepo := &mockProviderRepo{
		providers: map[uint]*domain.Provider{
			1: {ID: 1, Name: "Digiflazz", Code: "DIGIFLAZZ", IsActive: true},
			2: {ID: 2, Name: "Kiosgamer", Code: "KIOSGAMER", IsActive: true},
		},
	}

	gameFF := &domain.Game{ID: 1, Name: "Free Fire", Slug: "free-fire", IsActive: true}
	gameML := &domain.Game{ID: 2, Name: "Mobile Legends", Slug: "mobile-legends", IsActive: true}

	gameRepo := &mockGameRepo{
		games: map[uint]*domain.Game{
			1: gameFF,
			2: gameML,
		},
	}

	nominalRepo := &mockNominalRepo{
		nominals: map[uint]*domain.Nominal{
			// FF 50 DM: Game FF dan punya KiosgamerProductCode -> HARUS BERHASIL PINDAH
			101: {ID: 101, GameID: 1, Game: gameFF, Name: "Free Fire 50 Diamond", ProviderID: 1, ProviderProductCode: "FF50", KiosgamerProductCode: "1"},
			// FF 12 DM: Game FF tapi KiosgamerProductCode KOSONG -> HARUS FALLBACK (tetap di 1)
			102: {ID: 102, GameID: 1, Game: gameFF, Name: "Free Fire 12 Diamond", ProviderID: 1, ProviderProductCode: "FF12", KiosgamerProductCode: ""},
			// MLBB 86 DM: Game MLBB (Bukan game Kiosgamer) -> HARUS FALLBACK (tetap di 1)
			103: {ID: 103, GameID: 2, Game: gameML, Name: "Mobile Legends 86 Diamond", ProviderID: 1, ProviderProductCode: "ML86", KiosgamerProductCode: ""},
		},
	}

	svc := &gameService{
		gameRepo:     gameRepo,
		nominalRepo:  nominalRepo,
		providerRepo: providerRepo,
	}

	// Coba pindahkan semua 3 nominal ke Kiosgamer (ID 2)
	res, err := svc.BatchSwitchProvider([]uint{101, 102, 103}, 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if res.SwitchedCount != 1 {
		t.Errorf("expected SwitchedCount = 1, got %d", res.SwitchedCount)
	}

	if res.SkippedCount != 2 {
		t.Errorf("expected SkippedCount = 2, got %d", res.SkippedCount)
	}

	// Verifikasi hasil database
	if nominalRepo.nominals[101].ProviderID != 2 {
		t.Errorf("expected nominal 101 ProviderID = 2, got %d", nominalRepo.nominals[101].ProviderID)
	}
	if nominalRepo.nominals[102].ProviderID != 1 {
		t.Errorf("expected nominal 102 ProviderID = 1 (fallback FF tanpa SKU), got %d", nominalRepo.nominals[102].ProviderID)
	}
	if nominalRepo.nominals[103].ProviderID != 1 {
		t.Errorf("expected nominal 103 ProviderID = 1 (fallback MLBB bukan game Kiosgamer), got %d", nominalRepo.nominals[103].ProviderID)
	}
}

func TestBatchSwitchProvider_ToDigiflazz_AllSwitch(t *testing.T) {
	providerRepo := &mockProviderRepo{
		providers: map[uint]*domain.Provider{
			1: {ID: 1, Name: "Digiflazz", Code: "DIGIFLAZZ", IsActive: true},
			2: {ID: 2, Name: "Kiosgamer", Code: "KIOSGAMER", IsActive: true},
		},
	}

	gameFF := &domain.Game{ID: 1, Name: "Free Fire", Slug: "free-fire", IsActive: true}
	gameRepo := &mockGameRepo{games: map[uint]*domain.Game{1: gameFF}}

	nominalRepo := &mockNominalRepo{
		nominals: map[uint]*domain.Nominal{
			101: {ID: 101, GameID: 1, Game: gameFF, Name: "Free Fire 50 Diamond", ProviderID: 2, ProviderProductCode: "FF50"},
			102: {ID: 102, GameID: 1, Game: gameFF, Name: "Free Fire 12 Diamond", ProviderID: 2, ProviderProductCode: "FF12"},
		},
	}

	svc := &gameService{
		gameRepo:     gameRepo,
		nominalRepo:  nominalRepo,
		providerRepo: providerRepo,
	}

	// Pindahkan balik ke Digiflazz (ID 1) -> Semua nominal harus berhasil pindah
	res, err := svc.BatchSwitchProvider([]uint{101, 102}, 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if res.SwitchedCount != 2 {
		t.Errorf("expected SwitchedCount = 2, got %d", res.SwitchedCount)
	}

	if res.SkippedCount != 0 {
		t.Errorf("expected SkippedCount = 0, got %d", res.SkippedCount)
	}

	if nominalRepo.nominals[101].ProviderID != 1 || nominalRepo.nominals[102].ProviderID != 1 {
		t.Errorf("expected all nominals switched to ProviderID 1")
	}
}

// Mock ProviderProductRepository
type mockProviderProductRepo struct {
	products map[string]*domain.ProviderProduct // key: nominalID_providerID
}

func newMockProviderProductRepo() *mockProviderProductRepo {
	return &mockProviderProductRepo{products: make(map[string]*domain.ProviderProduct)}
}

func (m *mockProviderProductRepo) Create(pp *domain.ProviderProduct) error {
	key := fmt.Sprintf("%d_%d", pp.NominalID, pp.ProviderID)
	m.products[key] = pp
	return nil
}

func (m *mockProviderProductRepo) Upsert(pp *domain.ProviderProduct) error {
	key := fmt.Sprintf("%d_%d", pp.NominalID, pp.ProviderID)
	m.products[key] = pp
	return nil
}

func (m *mockProviderProductRepo) FindByNominalAndProvider(nominalID, providerID uint) (*domain.ProviderProduct, error) {
	key := fmt.Sprintf("%d_%d", nominalID, providerID)
	return m.products[key], nil
}

func (m *mockProviderProductRepo) FindByNominalAndProviderCode(nominalID uint, providerCode string) (*domain.ProviderProduct, error) {
	for _, pp := range m.products {
		if pp.NominalID == nominalID && pp.Provider != nil && pp.Provider.Code == providerCode {
			return pp, nil
		}
	}
	return nil, nil
}

func (m *mockProviderProductRepo) ListByNominalID(nominalID uint) ([]domain.ProviderProduct, error) {
	var list []domain.ProviderProduct
	for _, pp := range m.products {
		if pp.NominalID == nominalID {
			list = append(list, *pp)
		}
	}
	return list, nil
}

func (m *mockProviderProductRepo) ListByProviderID(providerID uint) ([]domain.ProviderProduct, error) {
	var list []domain.ProviderProduct
	for _, pp := range m.products {
		if pp.ProviderID == providerID {
			list = append(list, *pp)
		}
	}
	return list, nil
}
func (m *mockProviderProductRepo) UpdateCostPrice(uint, string, *float64) error { return nil }

func (m *mockProviderProductRepo) Delete(id uint) error {
	return nil
}

type mockGameSupportProvider struct {
	code string
}

func (p *mockGameSupportProvider) Code() string                                              { return p.code }
func (*mockGameSupportProvider) Purchase(context.Context, provider.PurchaseRequest) (*provider.Result, error)  { return nil, nil }
func (*mockGameSupportProvider) CheckStatus(context.Context, provider.StatusRequest) (*provider.Result, error) { return nil, nil }
func (*mockGameSupportProvider) Balance(context.Context) (float64, error)                    { return 0, nil }
func (*mockGameSupportProvider) Supports(gameSlug string) bool {
	return gameSlug == "free-fire" || gameSlug == "codm"
}

func TestBatchSwitchProvider_WithRegistryCapabilityGameSupport(t *testing.T) {
	providerRepo := &mockProviderRepo{
		providers: map[uint]*domain.Provider{
			1: {ID: 1, Name: "Digiflazz", Code: "DIGIFLAZZ", IsActive: true},
			2: {ID: 2, Name: "Kiosgamer", Code: "KIOSGAMER", IsActive: true},
		},
	}

	gameFF := &domain.Game{ID: 1, Name: "Free Fire", Slug: "free-fire", IsActive: true}
	gameML := &domain.Game{ID: 2, Name: "Mobile Legends", Slug: "mobile-legends", IsActive: true}

	gameRepo := &mockGameRepo{
		games: map[uint]*domain.Game{
			1: gameFF,
			2: gameML,
		},
	}

	nominalRepo := &mockNominalRepo{
		nominals: map[uint]*domain.Nominal{
			101: {ID: 101, GameID: 1, Game: gameFF, Name: "Free Fire 50 Diamond", ProviderID: 1, ProviderProductCode: "FF50", KiosgamerProductCode: "1"},
			102: {ID: 102, GameID: 1, Game: gameFF, Name: "Free Fire 12 Diamond", ProviderID: 1, ProviderProductCode: "FF12", KiosgamerProductCode: ""},
			103: {ID: 103, GameID: 2, Game: gameML, Name: "Mobile Legends 86 Diamond", ProviderID: 1, ProviderProductCode: "ML86", KiosgamerProductCode: ""},
		},
	}

	reg := provider.NewRegistry()
	_ = reg.Register(&mockGameSupportProvider{code: "KIOSGAMER"})

	svc := &gameService{
		gameRepo:         gameRepo,
		nominalRepo:      nominalRepo,
		providerRepo:     providerRepo,
		providerRegistry: reg,
	}

	res, err := svc.BatchSwitchProvider([]uint{101, 102, 103}, 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if res.SwitchedCount != 1 {
		t.Errorf("expected SwitchedCount = 1, got %d", res.SwitchedCount)
	}
	if res.SkippedCount != 2 {
		t.Errorf("expected SkippedCount = 2, got %d", res.SkippedCount)
	}
}

func TestDualWriteNominal_CreateAndUpdate(t *testing.T) {
	providerRepo := &mockProviderRepo{
		providers: map[uint]*domain.Provider{
			1: {ID: 1, Name: "Digiflazz", Code: "DIGIFLAZZ", IsActive: true},
			2: {ID: 2, Name: "Kiosgamer", Code: "KIOSGAMER", IsActive: true},
		},
	}
	gameFF := &domain.Game{ID: 1, Name: "Free Fire", Slug: "free-fire", IsActive: true}
	gameRepo := &mockGameRepo{games: map[uint]*domain.Game{1: gameFF}}
	nominalRepo := &mockNominalRepo{nominals: make(map[uint]*domain.Nominal)}
	ppRepo := newMockProviderProductRepo()

	svc := &gameService{
		gameRepo:            gameRepo,
		nominalRepo:         nominalRepo,
		providerRepo:        providerRepo,
		providerProductRepo: ppRepo,
	}

	// 1. Create nominal with both Digiflazz and Kiosgamer codes
	nom := &domain.Nominal{
		ID:                   201,
		GameID:               1,
		ProviderID:           1,
		Name:                 "FF 100 Diamond",
		BasePrice:            15000,
		ProviderProductCode:  "DF-FF100",
		KiosgamerProductCode: "KG-FF100",
		IsActive:             true,
	}

	if err := svc.CreateNominal(nom); err != nil {
		t.Fatalf("CreateNominal err: %v", err)
	}

	// Verify dual-write created provider_products rows
	dfPP := ppRepo.products["201_1"]
	if dfPP == nil || dfPP.ProductCode != "DF-FF100" {
		t.Fatalf("expected Digiflazz provider_product DF-FF100, got %+v", dfPP)
	}
	kgPP := ppRepo.products["201_2"]
	if kgPP == nil || kgPP.ProductCode != "KG-FF100" {
		t.Fatalf("expected Kiosgamer provider_product KG-FF100, got %+v", kgPP)
	}

	// 2. Update nominal with empty ProviderProductCode: MUST NOT overwrite existing column with empty (§3.6, §5)
	updateNom := &domain.Nominal{
		ID:                   201,
		GameID:               1,
		ProviderID:           1,
		Name:                 "FF 100 Diamond (Updated)",
		BasePrice:            16000,
		ProviderProductCode:  "", // empty in update payload
		KiosgamerProductCode: "KG-FF100-V2",
		IsActive:             true,
	}

	if err := svc.UpdateNominal(updateNom); err != nil {
		t.Fatalf("UpdateNominal err: %v", err)
	}

	// Verify legacy column was not overwritten with empty
	if updateNom.ProviderProductCode != "DF-FF100" {
		t.Fatalf("expected ProviderProductCode preserved as DF-FF100, got %q", updateNom.ProviderProductCode)
	}

	// Verify Kiosgamer was updated
	kgPPUpdated := ppRepo.products["201_2"]
	if kgPPUpdated == nil || kgPPUpdated.ProductCode != "KG-FF100-V2" {
		t.Fatalf("expected updated Kiosgamer code KG-FF100-V2, got %+v", kgPPUpdated)
	}
}

