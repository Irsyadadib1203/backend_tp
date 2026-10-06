package database

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

func setupTestDBForBackfill(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite in-memory db: %v", err)
	}

	err = db.AutoMigrate(
		&domain.Provider{},
		&domain.Nominal{},
		&domain.ProviderProduct{},
	)
	if err != nil {
		t.Fatalf("automigrate err: %v", err)
	}

	// Seed providers
	db.Create(&domain.Provider{ID: 1, Name: "Digiflazz", Code: "DIGIFLAZZ", IsActive: true})
	db.Create(&domain.Provider{ID: 2, Name: "Kiosgamer", Code: "KIOSGAMER", IsActive: true})

	// Seed nominals
	db.Create(&domain.Nominal{
		ID:                   1,
		ProviderID:           1,
		Name:                 "FF 50 Diamond",
		BasePrice:            6000,
		ProviderProductCode:  "DF-FF50",
		KiosgamerProductCode: "KG-FF50",
		IsActive:             true,
	})
	db.Create(&domain.Nominal{
		ID:                   2,
		ProviderID:           1,
		Name:                 "MLBB 86 Diamond",
		BasePrice:            18000,
		ProviderProductCode:  "DF-ML86",
		KiosgamerProductCode: "", // No Kiosgamer code
		IsActive:             true,
	})

	return db
}

func TestBackfillProviderProducts_DryRun(t *testing.T) {
	db := setupTestDBForBackfill(t)

	// Run in dry-run mode
	res, err := BackfillProviderProducts(db, true)
	if err != nil {
		t.Fatalf("backfill err: %v", err)
	}

	if res.TotalNominals != 2 {
		t.Errorf("expected 2 nominals, got %d", res.TotalNominals)
	}
	if res.DigiflazzCount != 2 {
		t.Errorf("expected 2 Digiflazz mappings, got %d", res.DigiflazzCount)
	}
	if res.KiosgamerCount != 1 {
		t.Errorf("expected 1 Kiosgamer mapping, got %d", res.KiosgamerCount)
	}
	if res.TotalCreated != 3 {
		t.Errorf("expected 3 to be created, got %d", res.TotalCreated)
	}

	// Verify that in dry-run mode, NO rows were written to provider_products
	var count int64
	db.Model(&domain.ProviderProduct{}).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 rows in DB during dry-run, found %d", count)
	}
}

func TestBackfillProviderProducts_ApplyAndIdempotent(t *testing.T) {
	db := setupTestDBForBackfill(t)

	// 1. Run live backfill
	res1, err := BackfillProviderProducts(db, false)
	if err != nil {
		t.Fatalf("backfill err: %v", err)
	}

	if res1.TotalCreated != 3 {
		t.Errorf("expected 3 rows created on first run, got %d", res1.TotalCreated)
	}

	// Check DB rows
	var count int64
	db.Model(&domain.ProviderProduct{}).Count(&count)
	if count != 3 {
		t.Errorf("expected 3 rows in DB, found %d", count)
	}

	// Verify specific row
	var pp domain.ProviderProduct
	err = db.Where("nominal_id = ? AND provider_id = ?", 1, 2).First(&pp).Error
	if err != nil || pp.ProductCode != "KG-FF50" {
		t.Fatalf("expected KG-FF50 for nominal 1 provider 2, got %+v (err: %v)", pp, err)
	}

	// 2. Run backfill a SECOND time (idempotency verification)
	res2, err := BackfillProviderProducts(db, false)
	if err != nil {
		t.Fatalf("second backfill err: %v", err)
	}

	if res2.TotalCreated != 0 {
		t.Errorf("expected 0 created on second run, got %d", res2.TotalCreated)
	}
	if res2.TotalSkipped != 3 {
		t.Errorf("expected 3 skipped on second run, got %d", res2.TotalSkipped)
	}

	// Verify count remains 3
	db.Model(&domain.ProviderProduct{}).Count(&count)
	if count != 3 {
		t.Errorf("expected count to remain 3, found %d", count)
	}
}
