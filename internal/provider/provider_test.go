package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/repository"
)

type testProvider struct{ code string }

func (p testProvider) Code() string                                              { return p.code }
func (testProvider) Purchase(context.Context, PurchaseRequest) (*Result, error)  { return nil, nil }
func (testProvider) CheckStatus(context.Context, StatusRequest) (*Result, error) { return nil, nil }
func (testProvider) Balance(context.Context) (float64, error)                    { return 0, nil }

type resolverProviderRepo struct{ providers map[uint]*domain.Provider }

func (r resolverProviderRepo) GetByCode(string) (*domain.Provider, error) { return nil, nil }
func (r resolverProviderRepo) GetByID(id uint) (*domain.Provider, error)  { return r.providers[id], nil }
func (resolverProviderRepo) List() ([]domain.Provider, error)             { return nil, nil }
func (resolverProviderRepo) Update(*domain.Provider) error                { return nil }
func (resolverProviderRepo) UpdateBalance(uint, float64) error            { return nil }
func (resolverProviderRepo) UpdatePriceSyncAt(uint, time.Time) error { return nil }
func (resolverProviderRepo) LogWebhook(*domain.WebhookLog) error          { return nil }
func (resolverProviderRepo) ListWebhookLogs(int, int, string) ([]domain.WebhookLog, int64, error) {
	return nil, 0, nil
}

func TestRegistryRegisterGetAndCodes(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testProvider{code: "digiflazz"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testProvider{code: " DIGIFLAZZ "}); err == nil {
		t.Fatal("expected duplicate error")
	}
	if _, ok := r.Get("DiGiFlAzZ"); !ok {
		t.Fatal("expected case-insensitive lookup")
	}
	codes := r.Codes()
	if len(codes) != 1 || codes[0] != DigiflazzCode {
		t.Fatalf("codes=%v", codes)
	}
}

func TestResolverLegacyColumnAndProviderPrecedence(t *testing.T) {
	repo := resolverProviderRepo{providers: map[uint]*domain.Provider{2: {ID: 2, Code: "KIOSGAMER"}, 3: {ID: 3, Code: ""}}}
	cases := []struct {
		name    string
		nominal *domain.Nominal
		want    string
	}{
		{"relation wins", &domain.Nominal{ProviderID: 2, Provider: &domain.Provider{Code: "DIGIFLAZZ"}}, DigiflazzCode},
		{"repository fallback", &domain.Nominal{ProviderID: 2}, KiosgamerCode},
		{"no provider uses Digiflazz default", &domain.Nominal{}, DigiflazzCode},
	}

	for _, tc := range []struct {
		name    string
		nominal *domain.Nominal
		repo    repository.ProviderRepository
	}{
		{"empty configured provider is rejected", &domain.Nominal{ProviderID: 3}, repo},
		{"missing configured provider is rejected", &domain.Nominal{ProviderID: 99}, repo},
		{"missing repository is rejected", &domain.Nominal{ProviderID: 2}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, err := ResolveProviderCode(tc.nominal, tc.repo); err == nil || code != "" {
				t.Fatalf("code=%q err=%v; configured provider must not fall back", code, err)
			}
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveProviderCode(tc.nominal, repo)
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v", got, err)
			}
		})
	}
	if code, err := ResolveProductCode(&domain.Nominal{ProviderProductCode: "DF", KiosgamerProductCode: "KG"}, KiosgamerCode); err != nil || code != "KG" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if code, err := ResolveProductCode(&domain.Nominal{ProviderProductCode: "DF", KiosgamerProductCode: "KG"}, DigiflazzCode); err != nil || code != "DF" {
		t.Fatalf("code=%q err=%v", code, err)
	}
}

func TestResolveProviderCodePropagatesLookupFailure(t *testing.T) {
	lookupErr := errors.New("database unavailable")
	repo := resolverProviderRepoError{err: lookupErr}
	if code, err := ResolveProviderCode(&domain.Nominal{ProviderID: 7}, repo); code != "" || !errors.Is(err, lookupErr) {
		t.Fatalf("code=%q err=%v", code, err)
	}
}

type resolverProviderRepoError struct{ err error }

func (r resolverProviderRepoError) GetByCode(string) (*domain.Provider, error) { return nil, r.err }
func (r resolverProviderRepoError) GetByID(uint) (*domain.Provider, error)     { return nil, r.err }
func (resolverProviderRepoError) List() ([]domain.Provider, error)             { return nil, nil }
func (resolverProviderRepoError) Update(*domain.Provider) error                { return nil }
func (resolverProviderRepoError) UpdateBalance(uint, float64) error            { return nil }
func (resolverProviderRepoError) UpdatePriceSyncAt(uint, time.Time) error { return nil }
func (resolverProviderRepoError) LogWebhook(*domain.WebhookLog) error          { return nil }
func (resolverProviderRepoError) ListWebhookLogs(int, int, string) ([]domain.WebhookLog, int64, error) {
	return nil, 0, nil
}

func TestDualReadResolveProductCode(t *testing.T) {
	t.Run("provider_products wins over legacy column", func(t *testing.T) {
		nom := &domain.Nominal{
			ProviderProductCode:  "LEGACY-DF",
			KiosgamerProductCode: "LEGACY-KG",
			ProviderProducts: []domain.ProviderProduct{
				{
					ProductCode: "NEW-DF-SKU",
					IsActive:    true,
					Provider:    &domain.Provider{Code: DigiflazzCode},
				},
				{
					ProductCode: "NEW-KG-SKU",
					IsActive:    true,
					Provider:    &domain.Provider{Code: KiosgamerCode},
				},
			},
		}

		codeDF, err := ResolveProductCode(nom, DigiflazzCode)
		if err != nil || codeDF != "NEW-DF-SKU" {
			t.Fatalf("expected NEW-DF-SKU, got %q (err: %v)", codeDF, err)
		}

		codeKG, err := ResolveProductCode(nom, KiosgamerCode)
		if err != nil || codeKG != "NEW-KG-SKU" {
			t.Fatalf("expected NEW-KG-SKU, got %q (err: %v)", codeKG, err)
		}
	})

	t.Run("inactive provider_products falls back to legacy", func(t *testing.T) {
		nom := &domain.Nominal{
			ProviderProductCode: "LEGACY-DF",
			ProviderProducts: []domain.ProviderProduct{
				{
					ProductCode: "INACTIVE-SKU",
					IsActive:    false,
					Provider:    &domain.Provider{Code: DigiflazzCode},
				},
			},
		}

		code, err := ResolveProductCode(nom, DigiflazzCode)
		if err != nil || code != "LEGACY-DF" {
			t.Fatalf("expected fallback to LEGACY-DF, got %q", code)
		}
	})

	t.Run("empty provider_products falls back to legacy", func(t *testing.T) {
		nom := &domain.Nominal{
			ProviderProductCode:  "FALLBACK-DF",
			KiosgamerProductCode: "FALLBACK-KG",
		}

		codeDF, err := ResolveProductCode(nom, DigiflazzCode)
		if err != nil || codeDF != "FALLBACK-DF" {
			t.Fatalf("expected FALLBACK-DF, got %q", codeDF)
		}

		codeKG, err := ResolveProductCode(nom, KiosgamerCode)
		if err != nil || codeKG != "FALLBACK-KG" {
			t.Fatalf("expected FALLBACK-KG, got %q", codeKG)
		}
	})
}

func TestProviderErrorUnwrapsCause(t *testing.T) {
	cause := &testError{"network"}
	err := &ProviderError{Status: StatusFailedHold, Kind: ErrorTemporary, ProviderStatus: "Pending", Cause: cause}
	if err.Error() != "network" || err.Unwrap() != cause {
		t.Fatalf("unexpected error: %v", err)
	}
}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }
