package service

import (
	"testing"
	"topup-backend/internal/domain"
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
