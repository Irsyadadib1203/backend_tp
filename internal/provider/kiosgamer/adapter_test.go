package kiosgamer

import (
	"context"
	"errors"
	"testing"
	"time"

	"topup-backend/internal/provider"
	"topup-backend/internal/service"
)

type fakeKiosgamer struct {
	place, poll, recent                *service.KiosgamerOrderResult
	placeErr, pollErr, recentErr       error
	placeCalls, pollCalls, recentCalls int
	appID                              int
}

func (*fakeKiosgamer) SaveCredentials(string, string) error                     { return nil }
func (*fakeKiosgamer) Status(context.Context) (*service.KiosgamerStatus, error) { return nil, nil }
func (*fakeKiosgamer) HealthCheck(context.Context) (*service.KiosgamerUserInfo, error) {
	return nil, nil
}
func (*fakeKiosgamer) EnsureSession(context.Context) (*service.KiosgamerUserInfo, error) {
	return nil, nil
}
func (*fakeKiosgamer) RecoverSession(context.Context) error   { return nil }
func (*fakeKiosgamer) GenerateTOTP(time.Time) (string, error) { return "", nil }
func (f *fakeKiosgamer) PlaceOrder(context.Context, string, string, string, string, string) (*service.KiosgamerOrderResult, error) {
	f.placeCalls++
	return f.place, f.placeErr
}
func (f *fakeKiosgamer) PollOrder(context.Context, string) (*service.KiosgamerOrderResult, error) {
	f.pollCalls++
	return f.poll, f.pollErr
}
func (f *fakeKiosgamer) CheckRecentOrder(_ context.Context, appID int) (*service.KiosgamerOrderResult, error) {
	f.recentCalls++
	f.appID = appID
	return f.recent, f.recentErr
}
func (*fakeKiosgamer) KeepAlive(context.Context) error { return nil }
func (*fakeKiosgamer) FetchCatalog(context.Context, string) ([]service.KiosgamerCatalogItem, error) {
	return nil, nil
}
func (*fakeKiosgamer) AutoSyncMapping(context.Context, uint, string) (*service.KiosgamerSyncResult, error) {
	return nil, nil
}
func (*fakeKiosgamer) UpdateNominalKiosgamerCode(uint, string) error { return nil }

func TestPurchasePreservesSafeRetry(t *testing.T) {
	fake := &fakeKiosgamer{poll: &service.KiosgamerOrderResult{OrderID: "ORDER-1", Status: "pending"}}
	result, err := New(fake).Purchase(context.Background(), provider.PurchaseRequest{ExistingProviderOrderID: "ORDER-1"})
	if err != nil || result.Status != provider.StatusPending || fake.pollCalls != 1 || fake.placeCalls != 0 {
		t.Fatalf("result=%+v err=%v poll=%d place=%d", result, err, fake.pollCalls, fake.placeCalls)
	}
}

func TestPurchaseEmptySKUReturnsConfigError(t *testing.T) {
	t.Run("empty SKU without existing order returns Konfigurasi Error", func(t *testing.T) {
		fake := &fakeKiosgamer{}
		result, err := New(fake).Purchase(context.Background(), provider.PurchaseRequest{ProductCode: "", ProductName: "Test Product"})
		if result != nil {
			t.Fatalf("expected nil result: %+v", result)
		}
		providerErr, ok := err.(*provider.ProviderError)
		if !ok || providerErr.Kind != provider.ErrorConfiguration || providerErr.ProviderStatus != "Konfigurasi Error" || fake.placeCalls != 0 {
			t.Fatalf("err=%#v place=%d", err, fake.placeCalls)
		}
	})

	t.Run("existing order ID bypasses SKU guard (safe-retry)", func(t *testing.T) {
		fake := &fakeKiosgamer{poll: &service.KiosgamerOrderResult{OrderID: "ORDER-EXISTING", Status: "pending"}}
		// ExistingProviderOrderID present but ProductCode empty — must still poll without error.
		result, err := New(fake).Purchase(context.Background(), provider.PurchaseRequest{ExistingProviderOrderID: "ORDER-EXISTING"})
		if err != nil || result == nil || result.Status != provider.StatusPending || fake.pollCalls != 1 || fake.placeCalls != 0 {
			t.Fatalf("result=%+v err=%v poll=%d place=%d", result, err, fake.pollCalls, fake.placeCalls)
		}
	})
}

func TestKiosgamerErrorClassificationOrder(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		want   provider.Status
		status string
	}{
		{"challenge precedes generic error", service.ErrKiosgamerChallengeRequired, provider.StatusFailedHold, "Challenge Required"},
		{"session", service.ErrKiosgamerSessionExpired, provider.StatusFailedHold, "Session Error"},
		{"balance", errors.New("shell balance unavailable"), provider.StatusFailedHold, "Provider Pending"},
		{"fatal customer", errors.New("player id tidak ditemukan"), provider.StatusFailedFinal, "Gagal"},
		{"unknown holds", errors.New("unexpected upstream error"), provider.StatusFailedHold, "Provider Pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeKiosgamer{placeErr: tc.err}
			result, err := New(fake).Purchase(context.Background(), provider.PurchaseRequest{ProductCode: "KG-SKU"})
			if result != nil {
				t.Fatalf("expected nil result: %+v", result)
			}
			providerErr, ok := err.(*provider.ProviderError)
			if !ok || providerErr.Status != tc.want || providerErr.ProviderStatus != tc.status || fake.placeCalls != 1 {
				t.Fatalf("err=%#v place=%d", err, fake.placeCalls)
			}
		})
	}
}

func TestKiosgamerResultAndStatusCheckMapping(t *testing.T) {
	cases := []struct {
		name, status, message string
		want                  provider.Status
		ps                    string
	}{
		{"success", "success", "ok", provider.StatusSuccess, "Sukses"},
		{"failed hold", "failed", "saldo shell habis", provider.StatusFailedHold, "Pending (Kendala Provider)"},
		{"failed final", "failed", "id salah", provider.StatusFailedFinal, "Gagal"},
		{"pending", "pending", "wait", provider.StatusPending, "Pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeKiosgamer{place: &service.KiosgamerOrderResult{OrderID: "ORDER-1", Status: tc.status, Message: tc.message, SerialNumber: "SN-1"}}
			result, err := New(fake).Purchase(context.Background(), provider.PurchaseRequest{ProductCode: "KG-SKU"})
			if err != nil || result.Status != tc.want || result.ProviderStatus != tc.ps || result.ProviderOrderID != "ORDER-1" || len(result.Raw) == 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}

	t.Run("CODM check uses legacy app id", func(t *testing.T) {
		fake := &fakeKiosgamer{recent: &service.KiosgamerOrderResult{Status: "pending"}}
		result, err := New(fake).CheckStatus(context.Background(), provider.StatusRequest{GameSlug: "call-of-duty-mobile"})
		if err != nil || result.Status != provider.StatusPending || fake.recentCalls != 1 || fake.appID != codmAppID || fake.placeCalls != 0 {
			t.Fatalf("result=%+v err=%v app=%d", result, err, fake.appID)
		}
	})
}

func TestKiosgamerGameSupport(t *testing.T) {
	a := New(&fakeKiosgamer{})
	if !a.Supports("free-fire") || !a.Supports("codm") || a.Supports("mobile-legends") {
		t.Fatal("unexpected game support")
	}
}
