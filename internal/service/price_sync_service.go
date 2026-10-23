package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

// PriceSyncService mengambil katalog harga dari satu provider lalu menyimpannya
// ke provider_products.cost_price. Service ini TIDAK menyentuh tabel nominals,
// jadi harga jual tidak pernah berubah karena sinkron ini.
type PriceSyncService interface {
	SyncProvider(ctx context.Context, providerCode string) error
	SyncAllProviders(ctx context.Context) error
	SupportsCatalog(providerCode string) bool
	SetNotifier(notifier PriceChangeNotifier)
}

// BasePriceSyncer dipenuhi oleh GameService.
type BasePriceSyncer interface {
	SyncBasePriceFromActiveProvider(nominalID uint) (bool, error)
}

// PriceChangeNotifier mengirimkan notifikasi saat harga modal provider berubah.
type PriceChangeNotifier interface {
	SendPriceChangeAlert(providerName, providerCode string, changes []PriceChangeItem) error
}

type priceSyncService struct {
	providerRepo        repository.ProviderRepository
	providerProductRepo repository.ProviderProductRepository
	registry            *provider.Registry
	basePriceSyncer     BasePriceSyncer
	notifier            PriceChangeNotifier
}

func NewPriceSyncService(
	providerRepo repository.ProviderRepository,
	providerProductRepo repository.ProviderProductRepository,
	registry *provider.Registry,
	basePriceSyncer BasePriceSyncer,
) PriceSyncService {
	return &priceSyncService{
		providerRepo:        providerRepo,
		providerProductRepo: providerProductRepo,
		registry:            registry,
		basePriceSyncer:     basePriceSyncer,
	}
}

func (s *priceSyncService) SetNotifier(notifier PriceChangeNotifier) {
	s.notifier = notifier
}

// SupportsCatalog: true jika adapter provider punya SyncCatalog.
func (s *priceSyncService) SupportsCatalog(providerCode string) bool {
	adapter, ok := s.registry.Get(strings.ToUpper(strings.TrimSpace(providerCode)))
	if !ok {
		return false
	}
	_, ok = adapter.(provider.CatalogSyncer)
	return ok
}

func (s *priceSyncService) SyncProvider(ctx context.Context, providerCode string) error {
	code := strings.ToUpper(strings.TrimSpace(providerCode))

	dbProvider, err := s.providerRepo.GetByCode(code)
	if err != nil || dbProvider == nil {
		return fmt.Errorf("provider %s tidak ditemukan di database", code)
	}

	adapter, ok := s.registry.Get(code)
	if !ok {
		return fmt.Errorf("provider %s belum terdaftar di registry", code)
	}

	syncer, ok := adapter.(provider.CatalogSyncer)
	if !ok {
		return fmt.Errorf("provider %s tidak mendukung sinkron katalog", code)
	}

	items, err := syncer.SyncCatalog(ctx)
	if err != nil {
		return fmt.Errorf("ambil katalog %s: %w", code, err)
	}
	// Pengaman: katalog kosong biasanya berarti respons bermasalah. Batalkan agar
	// harga lama tidak terhapus semua.
	if len(items) == 0 {
		return errors.New("katalog kosong, sinkron dibatalkan")
	}

	catalog := make(map[string]provider.CatalogItem, len(items))
	for _, it := range items {
		catalog[strings.TrimSpace(it.ProductCode)] = it
	}

	rows, err := s.providerProductRepo.ListByProviderID(dbProvider.ID)
	if err != nil {
		return fmt.Errorf("baca provider_products %s: %w", code, err)
	}

	changed, skipped, repriced := 0, 0, 0
	var priceChanges []PriceChangeItem

	for _, row := range rows {
		// Toggle mati => lewati, harga lama dibiarkan
		if !row.AutoSyncPrice {
			skipped++
			continue
		}

		var newPrice *float64
		if it, found := catalog[strings.TrimSpace(row.ProductCode)]; found && it.IsActive {
			price := it.BasePrice
			newPrice = &price
		}

		if sameCostPrice(row.CostPrice, newPrice) {
			continue
		}

		oldCost := 0.0
		if row.CostPrice != nil {
			oldCost = *row.CostPrice
		}
		newCost := 0.0
		if newPrice != nil {
			newCost = *newPrice
		}

		categoryName := "Umum"
		if row.Nominal != nil && row.Nominal.Game != nil && row.Nominal.Game.Name != "" {
			categoryName = row.Nominal.Game.Name
		} else if row.Nominal != nil && row.Nominal.Name != "" {
			categoryName = row.Nominal.Name
		}

		if oldCost > 0 && newCost > 0 {
			priceChanges = append(priceChanges, PriceChangeItem{
				Category:    categoryName,
				ProductCode: row.ProductCode,
				OldPrice:    oldCost,
				NewPrice:    newCost,
			})
		}

		if err := s.providerProductRepo.UpdateCostPrice(dbProvider.ID, row.ProductCode, newPrice); err != nil {
			log.Printf("[PriceSync] %s: gagal update %s: %v", code, row.ProductCode, err)
			continue
		}
		changed++

		// Ikutkan base_price & harga jual jika provider ini adalah provider
		// aktif nominal tersebut (dicek di dalam SyncBasePriceFromActiveProvider).
		if s.basePriceSyncer != nil && newPrice != nil {
			ok, err := s.basePriceSyncer.SyncBasePriceFromActiveProvider(row.NominalID)
			if err != nil {
				log.Printf("[PriceSync] %s: gagal update harga jual nominal %d: %v", code, row.NominalID, err)
			} else if ok {
				repriced++
			}
		}
	}

	// Hanya diisi jika sinkron berhasil.
	if err := s.providerRepo.UpdatePriceSyncAt(dbProvider.ID, time.Now()); err != nil {
		return fmt.Errorf("simpan last_price_sync_at %s: %w", code, err)
	}

	// Kirim alert Telegram jika ada perubahan harga dari provider
	if len(priceChanges) > 0 && s.notifier != nil {
		if err := s.notifier.SendPriceChangeAlert(dbProvider.Name, dbProvider.Code, priceChanges); err != nil {
			log.Printf("[PriceSync] %s: gagal kirim alert perubahan harga ke Telegram: %v", code, err)
		}
	}

	log.Printf("[PriceSync] %s selesai: %d dari %d baris berubah, %d harga jual ikut diperbarui, %d dilewati (toggle off)", code, changed, len(rows), repriced, skipped)
	return nil
}

func (s *priceSyncService) SyncAllProviders(ctx context.Context) error {
	providers, err := s.providerRepo.List()
	if err != nil {
		return err
	}
	for _, p := range providers {
		if !p.IsActive || !s.SupportsCatalog(p.Code) {
			continue
		}
		if err := s.SyncProvider(ctx, p.Code); err != nil {
			log.Printf("[PriceSync] SyncAll %s gagal: %v", p.Code, err)
		}
	}
	return nil
}

func sameCostPrice(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) < 0.005
}
