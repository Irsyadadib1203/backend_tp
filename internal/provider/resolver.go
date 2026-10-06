package provider

import (
	"fmt"
	"strings"

	"topup-backend/internal/domain"
	"topup-backend/internal/repository"
)

const DigiflazzCode = "DIGIFLAZZ"
const KiosgamerCode = "KIOSGAMER"

// ResolveProviderCode preserves the Digiflazz default only for truly legacy
// nominals with no provider assignment. A nominal that names a provider must
// resolve to that provider or fail; silently falling back would send an order
// to the wrong upstream.
func ResolveProviderCode(nominal *domain.Nominal, providerRepo repository.ProviderRepository) (string, error) {
	if nominal == nil {
		return "", fmt.Errorf("nominal is nil")
	}
	if nominal.Provider != nil && strings.TrimSpace(nominal.Provider.Code) != "" {
		return normalizeCode(nominal.Provider.Code), nil
	}
	if nominal.ProviderID > 0 {
		if providerRepo == nil {
			return "", fmt.Errorf("provider %d cannot be resolved: repository is not configured", nominal.ProviderID)
		}
		p, err := providerRepo.GetByID(nominal.ProviderID)
		if err != nil {
			return "", fmt.Errorf("provider %d cannot be resolved: %w", nominal.ProviderID, err)
		}
		if p == nil || strings.TrimSpace(p.Code) == "" {
			return "", fmt.Errorf("provider %d has no provider code", nominal.ProviderID)
		}
		return normalizeCode(p.Code), nil
	}
	return DigiflazzCode, nil
}

// ResolveProductCode performs dual-read (§3.6): checks provider_products first,
// and falls back to legacy nominal columns (ProviderProductCode for DIGIFLAZZ,
// KiosgamerProductCode for KIOSGAMER).
func ResolveProductCode(nominal *domain.Nominal, providerCode string, repos ...repository.ProviderProductRepository) (string, error) {
	if nominal == nil {
		return "", fmt.Errorf("nominal is nil")
	}
	normCode := normalizeCode(providerCode)

	// 1. Dual-read: check preloaded provider_products slice first
	for _, pp := range nominal.ProviderProducts {
		if !pp.IsActive || strings.TrimSpace(pp.ProductCode) == "" {
			continue
		}
		// Match by Provider relation code if loaded
		if pp.Provider != nil && normalizeCode(pp.Provider.Code) == normCode {
			return pp.ProductCode, nil
		}
		// Match if nominal.Provider is loaded and IDs match
		if nominal.Provider != nil && nominal.Provider.ID == pp.ProviderID && normalizeCode(nominal.Provider.Code) == normCode {
			return pp.ProductCode, nil
		}
	}

	// 2. Dual-read: check repository if provided and nominal has an ID
	if len(repos) > 0 && repos[0] != nil && nominal.ID > 0 {
		if pp, err := repos[0].FindByNominalAndProviderCode(nominal.ID, normCode); err == nil && pp != nil {
			if strings.TrimSpace(pp.ProductCode) != "" {
				return pp.ProductCode, nil
			}
		}
	}

	// 3. Fallback to legacy nominal columns
	if normCode == KiosgamerCode {
		return nominal.KiosgamerProductCode, nil
	}
	return nominal.ProviderProductCode, nil
}
