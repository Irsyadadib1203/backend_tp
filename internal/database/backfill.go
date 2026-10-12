package database

import (
	"fmt"
	"log"
	"strings"

	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type BackfillResult struct {
	TotalNominals  int  `json:"total_nominals"`
	DigiflazzCount int  `json:"digiflazz_count"`
	KiosgamerCount int  `json:"kiosgamer_count"`
	TotalCreated   int  `json:"total_created"`
	TotalSkipped   int  `json:"total_skipped"`
	IsDryRun       bool `json:"is_dry_run"`
	// RepairedKiosgamer: baris provider_products Kiosgamer yang berisi SKU Digiflazz dan sudah dibetulkan.
	RepairedKiosgamer int `json:"repaired_kiosgamer"`
	// SuspectOther: baris provider lain yang memakai SKU yang sama dengan SKU Digiflazz (perlu dicek manual).
	SuspectOther int `json:"suspect_other"`
}

// BackfillProviderProducts idempotently populates provider_products from legacy nominal columns (§3.6).
// In dry-run mode, it only inspects and counts the rows that would be created without writing to DB.
func BackfillProviderProducts(db *gorm.DB, dryRun bool) (*BackfillResult, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}

	result := &BackfillResult{IsDryRun: dryRun}

	// 1. Resolve default Digiflazz provider ID
	var digiProvider domain.Provider
	_ = db.Where("UPPER(code) = ?", "DIGIFLAZZ").First(&digiProvider).Error

	// 2. Resolve Kiosgamer provider ID
	var kiosProvider domain.Provider
	_ = db.Where("UPPER(code) = ?", "KIOSGAMER").First(&kiosProvider).Error

	// 3. Load all active/inactive nominals
	var nominals []domain.Nominal
	if err := db.Find(&nominals).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch nominals: %w", err)
	}
	result.TotalNominals = len(nominals)

	for _, nom := range nominals {
		// A. Backfill Digiflazz SKU (ProviderProductCode)
		if strings.TrimSpace(nom.ProviderProductCode) != "" {
			// ProviderProductCode selalu SKU Digiflazz, apa pun provider nominal saat ini.
			pID := digiProvider.ID
			if pID > 0 {
				var count int64
				db.Model(&domain.ProviderProduct{}).
					Where("nominal_id = ? AND provider_id = ?", nom.ID, pID).
					Count(&count)

				if count > 0 {
					result.TotalSkipped++
				} else {
					result.DigiflazzCount++
					result.TotalCreated++
					if !dryRun {
						costPrice := nom.BasePrice
						pp := domain.ProviderProduct{
							NominalID:   nom.ID,
							ProviderID:  pID,
							ProductCode: strings.TrimSpace(nom.ProviderProductCode),
							CostPrice:   &costPrice,
							IsActive:    nom.IsActive,
							Priority:    0,
						}
						if err := db.Create(&pp).Error; err != nil {
							log.Printf("[Backfill] Warning: failed to insert Digiflazz provider_product for nominal %d: %v", nom.ID, err)
						}
					}
				}
			}
		}

		// B. Backfill Kiosgamer SKU (KiosgamerProductCode)
		if strings.TrimSpace(nom.KiosgamerProductCode) != "" && kiosProvider.ID > 0 {
			var count int64
			db.Model(&domain.ProviderProduct{}).
				Where("nominal_id = ? AND provider_id = ?", nom.ID, kiosProvider.ID).
				Count(&count)

			if count > 0 {
				result.TotalSkipped++
			} else {
				result.KiosgamerCount++
				result.TotalCreated++
				if !dryRun {
					pp := domain.ProviderProduct{
						NominalID:   nom.ID,
						ProviderID:  kiosProvider.ID,
						ProductCode: strings.TrimSpace(nom.KiosgamerProductCode),
						IsActive:    nom.IsActive,
						Priority:    0,
					}
					if err := db.Create(&pp).Error; err != nil {
						log.Printf("[Backfill] Warning: failed to insert Kiosgamer provider_product for nominal %d: %v", nom.ID, err)
					}
				}
			}
		}
	}

	if !dryRun {
		repairProviderProducts(db, digiProvider, kiosProvider, result)
	}

	mode := "APPLIED"
	if dryRun {
		mode = "DRY-RUN (no database changes made)"
	}
	log.Printf("[Backfill] %s: Total Nominals=%d, Created=%d (Digiflazz=%d, Kiosgamer=%d), Skipped=%d, RepairedKiosgamer=%d, SuspectOther=%d",
		mode, result.TotalNominals, result.TotalCreated, result.DigiflazzCount, result.KiosgamerCount, result.TotalSkipped, result.RepairedKiosgamer, result.SuspectOther)

	return result, nil
}

// repairProviderProducts membetulkan data lama yang salah: SKU Digiflazz pernah
// tersimpan di provider_products milik provider lain (provider nominal saat itu).
//   - Kiosgamer: diganti dengan kiosgamer_product_code bila ada.
//   - Provider lain: hanya dicatat di log (SKU aslinya tidak diketahui), isi
//     manual lewat PUT /api/v1/admin/nominals/:id/provider-products.
func repairProviderProducts(db *gorm.DB, digi, kios domain.Provider, result *BackfillResult) {
	if digi.ID == 0 {
		return
	}
	var nominals []domain.Nominal
	if err := db.Where("TRIM(provider_product_code) <> ''").Find(&nominals).Error; err != nil {
		log.Printf("[Backfill] repair: gagal membaca nominals: %v", err)
		return
	}
	for _, nom := range nominals {
		digiCode := strings.TrimSpace(nom.ProviderProductCode)
		var rows []domain.ProviderProduct
		if err := db.Where("nominal_id = ?", nom.ID).Find(&rows).Error; err != nil {
			continue
		}
		for _, pp := range rows {
			if pp.ProviderID == digi.ID || strings.TrimSpace(pp.ProductCode) != digiCode {
				continue
			}
			if kios.ID > 0 && pp.ProviderID == kios.ID {
				kiosCode := strings.TrimSpace(nom.KiosgamerProductCode)
				if kiosCode != "" && kiosCode != digiCode {
					if err := db.Model(&domain.ProviderProduct{}).Where("id = ?", pp.ID).Update("product_code", kiosCode).Error; err == nil {
						result.RepairedKiosgamer++
						log.Printf("[Backfill] repair: nominal %d Kiosgamer SKU %q diganti %q", nom.ID, digiCode, kiosCode)
					}
					continue
				}
			}
			result.SuspectOther++
			log.Printf("[Backfill] PERINGATAN: nominal %d provider_id=%d memakai SKU yang sama dengan SKU Digiflazz (%q). Periksa dan ganti lewat PUT /api/v1/admin/nominals/%d/provider-products", nom.ID, pp.ProviderID, digiCode, nom.ID)
		}
	}
}
