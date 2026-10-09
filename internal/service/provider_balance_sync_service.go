package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
)

// ProviderBalanceStore sengaja sempit (hanya dua method). Dengan begitu
// repository provider yang sudah ada cukup ditambah method UpdateBalanceSnapshot
// tanpa mengubah interface ProviderRepository, dan fake di file tes tidak perlu diubah.
type ProviderBalanceStore interface {
	GetByCode(code string) (*domain.Provider, error)
	UpdateBalanceSnapshot(id uint, balance float64, checkedAt time.Time) error
}

// BalanceSyncService mengambil saldo satu provider lalu menyimpannya ke
// providers.balance dan providers.balance_checked_at.
type BalanceSyncService interface {
	SyncProviderBalance(ctx context.Context, providerCode string) error
}

type balanceSyncService struct {
	store    ProviderBalanceStore
	registry *provider.Registry
}

func NewBalanceSyncService(store ProviderBalanceStore, registry *provider.Registry) BalanceSyncService {
	return &balanceSyncService{store: store, registry: registry}
}

func (s *balanceSyncService) SyncProviderBalance(ctx context.Context, providerCode string) error {
	code := strings.ToUpper(strings.TrimSpace(providerCode))

	rec, err := s.store.GetByCode(code)
	if err != nil || rec == nil {
		return fmt.Errorf("provider %s tidak ditemukan di database", code)
	}

	adapter, ok := s.registry.Get(code)
	if !ok {
		return fmt.Errorf("provider %s belum terdaftar di registry", code)
	}

	balance, err := adapter.Balance(ctx)
	if err != nil {
		return fmt.Errorf("ambil saldo %s: %w", code, err)
	}

	// Hanya diisi jika pengambilan saldo berhasil, jadi balance_checked_at
	// selalu berarti "saldo terakhir yang valid diambil pada jam ini".
	if err := s.store.UpdateBalanceSnapshot(rec.ID, balance, time.Now()); err != nil {
		return fmt.Errorf("simpan saldo %s: %w", code, err)
	}
	return nil
}