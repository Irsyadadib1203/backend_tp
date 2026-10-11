package provider

import (
	"testing"

	"topup-backend/internal/domain"
)

func TestResolveProductCodeNeverLeaksDigiflazzSKUToOtherProviders(t *testing.T) {
	nominal := &domain.Nominal{Name: "86 Diamonds", ProviderProductCode: "ML86", KiosgamerProductCode: "12345"}
	if code, err := ResolveProductCode(nominal, "FFZSTORE"); err == nil {
		t.Fatalf("expected error for provider without mapping, got code %q", code)
	}
	if code, err := ResolveProductCode(nominal, KiosgamerCode); err != nil || code != "12345" {
		t.Fatalf("kiosgamer code = %q, err = %v", code, err)
	}
	if code, err := ResolveProductCode(nominal, DigiflazzCode); err != nil || code != "ML86" {
		t.Fatalf("digiflazz code = %q, err = %v", code, err)
	}
}

func TestValidateReferenceTemplate(t *testing.T) {
	valid := []string{"", "INV-{date}-{random}", "REF_{provider_code}({customer_id})"}
	for _, template := range valid {
		if err := ValidateReferenceTemplate(template); err != nil {
			t.Errorf("template %q should be valid: %v", template, err)
		}
	}
	invalid := []string{"INV-{invoice}", "INV {date}", "INV/{date}", "INV-{date", "INV-date}"}
	for _, template := range invalid {
		if err := ValidateReferenceTemplate(template); err == nil {
			t.Errorf("template %q should be rejected", template)
		}
	}
}
