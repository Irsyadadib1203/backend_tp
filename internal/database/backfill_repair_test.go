package database

import (
	"testing"

	"topup-backend/internal/domain"
)

// Regresi: SKU Digiflazz dulu disimpan ke provider milik nominal saat ini.
func TestBackfillKeepsDigiflazzSKUUnderDigiflazzWhenNominalUsesKiosgamer(t *testing.T) {
	db := setupTestDBForBackfill(t)
	db.Create(&domain.Nominal{ID: 3, ProviderID: 2, Name: "FF 100", BasePrice: 1, ProviderProductCode: "DF-FF100", KiosgamerProductCode: "KG-FF100", IsActive: true})

	if _, err := BackfillProviderProducts(db, false); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var df, kg domain.ProviderProduct
	if err := db.Where("nominal_id = ? AND provider_id = ?", 3, 1).First(&df).Error; err != nil || df.ProductCode != "DF-FF100" {
		t.Fatalf("digiflazz mapping = %+v err=%v", df, err)
	}
	if err := db.Where("nominal_id = ? AND provider_id = ?", 3, 2).First(&kg).Error; err != nil || kg.ProductCode != "KG-FF100" {
		t.Fatalf("kiosgamer mapping = %+v err=%v", kg, err)
	}
}

func TestBackfillRepairsKiosgamerRowPollutedWithDigiflazzSKU(t *testing.T) {
	db := setupTestDBForBackfill(t)
	db.Create(&domain.Nominal{ID: 4, ProviderID: 2, Name: "FF 200", BasePrice: 1, ProviderProductCode: "DF-FF200", KiosgamerProductCode: "KG-FF200", IsActive: true})
	db.Create(&domain.ProviderProduct{NominalID: 4, ProviderID: 2, ProductCode: "DF-FF200", IsActive: true}) // data lama yang salah

	res, err := BackfillProviderProducts(db, false)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var kg domain.ProviderProduct
	if err := db.Where("nominal_id = ? AND provider_id = ?", 4, 2).First(&kg).Error; err != nil || kg.ProductCode != "KG-FF200" {
		t.Fatalf("kiosgamer mapping not repaired: %+v err=%v", kg, err)
	}
	if res.RepairedKiosgamer != 1 {
		t.Fatalf("RepairedKiosgamer = %d, want 1", res.RepairedKiosgamer)
	}
}
