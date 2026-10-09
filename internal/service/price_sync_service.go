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
}

type priceSyncService struct {
	providerRepo        repository.ProviderRepository
	providerProductRepo repository.ProviderProductRepository
	registry            *provider.Registry
}

func NewPriceSyncService(
	providerRepo repository.ProviderRepository,
	providerProductRepo repository.ProviderProductRepository,
	registry *provider.Registry,
) PriceSyncService {
	return &priceSyncService{
		providerRepo:        providerRepo,
		providerProductRepo: providerProductRepo,
		registry:            registry,
	}
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

	changed := 0
	for _, row := range rows {
		// Produk tidak ada di katalog, atau sedang nonaktif di provider => harga NULL
		// supaya tidak ikut dihitung sebagai "termurah".
		var newPrice *float64
		if it, found := catalog[strings.TrimSpace(row.ProductCode)]; found && it.IsActive {
			price := it.BasePrice
			newPrice = &price
		}

		if sameCostPrice(row.CostPrice, newPrice) {
			continue // tidak berubah, tidak perlu menulis ke database
		}

		if err := s.providerProductRepo.UpdateCostPrice(dbProvider.ID, row.ProductCode, newPrice); err != nil {
			log.Printf("[PriceSync] %s: gagal update %s: %v", code, row.ProductCode, err)
			continue
		}
		changed++
	}

	// Hanya diisi jika sinkron berhasil.
	if err := s.providerRepo.UpdatePriceSyncAt(dbProvider.ID, time.Now()); err != nil {
		return fmt.Errorf("simpan last_price_sync_at %s: %w", code, err)
	}

	log.Printf("[PriceSync] %s selesai: %d dari %d baris berubah", code, changed, len(rows))
	return nil
}

func sameCostPrice(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) < 0.005
}