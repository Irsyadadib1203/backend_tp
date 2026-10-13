package service

import (
	"strings"
	"testing"

	"topup-backend/internal/domain"
)

func TestLimitReferenceKeepsRandomSuffix(t *testing.T) {
	long := strings.Repeat("A", 70) + "-ABC123"
	got := limitReference(long, 50)
	if len(got) != 50 || !strings.HasSuffix(got, "-ABC123") {
		t.Fatalf("limitReference = %q (len %d)", got, len(got))
	}
	if short := limitReference("INV-1", 50); short != "INV-1" {
		t.Fatalf("short reference changed: %q", short)
	}
}

func TestProviderReferenceLegacyWebUsesRefID(t *testing.T) {
	legacy := &domain.Transaction{Source: domain.SourceWeb, RefID: "REF-WEB"}
	if legacy.ProviderReference() != "REF-WEB" {
		t.Fatalf("legacy web transaction should use RefID, got %q", legacy.ProviderReference())
	}
}

func TestNewWebOrderKeepsRefInProviderRefID(t *testing.T) {
	// Order web baru: ProviderRefID berisi ref untuk provider, ProviderOrderID kosong sampai ada respons provider.
	web := &domain.Transaction{Source: domain.SourceWeb, ProviderRefID: "REF-NEW"}
	if web.ProviderReference() != "REF-NEW" {
		t.Fatalf("provider ref = %q", web.ProviderReference())
	}
	if hasProviderOrder(web) || providerOrderMayExist(web) {
		t.Fatal("unsubmitted order must be sent as a new order, not polled")
	}
}

func TestProviderReferenceH2HUsesProviderRefID(t *testing.T) {
	// Order H2H baru: ref_id milik mitra, ProviderRefID berisi ref untuk provider, ProviderOrderID kosong.
	h2h := &domain.Transaction{Source: domain.SourceH2H, RefID: "PARTNER-1", ProviderRefID: "REF-PROVIDER"}
	if h2h.ProviderReference() != "REF-PROVIDER" {
		t.Fatalf("h2h should send ProviderRefID as provider ref, got %q", h2h.ProviderReference())
	}
	if h2h.RealProviderOrderID() != "" || hasProviderOrder(h2h) {
		t.Fatal("unsubmitted order must not count as a real provider order")
	}
}

func TestProviderReferenceH2HSurvivesRealOrderID(t *testing.T) {
	h2h := &domain.Transaction{
		Source: domain.SourceH2H, RefID: "PARTNER-1", ProviderRefID: "REF-PROVIDER", ProviderOrderID: "KG-777",
		ProviderCallbackData: `{"action":"fulfill","request":{"ref_id":"REF-PROVIDER"},"response":{"order_id":"KG-777"}}`,
	}
	if h2h.ProviderReference() != "REF-PROVIDER" {
		t.Fatalf("provider ref = %q", h2h.ProviderReference())
	}
	if h2h.RealProviderOrderID() != "KG-777" || !hasProviderOrder(h2h) {
		t.Fatal("real provider order id should be detected")
	}
}

func TestHasProviderOrderForWebTransactions(t *testing.T) {
	for _, id := range []string{"", "-", "  "} {
		if hasProviderOrder(&domain.Transaction{Source: domain.SourceWeb, ProviderOrderID: id}) {
			t.Errorf("provider order id %q should not count as submitted", id)
		}
	}
	if !hasProviderOrder(&domain.Transaction{Source: domain.SourceWeb, ProviderOrderID: "KG123"}) {
		t.Error("real provider order id should count as submitted")
	}
}
