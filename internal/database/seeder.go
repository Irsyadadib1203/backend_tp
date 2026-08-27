package database

import (
	"log"
	"time"

	"gorm.io/gorm"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/pkg/utils"
)

func SeedInitialData(db *gorm.DB) {
	// ================================================================
	// PENTING: Seeder ini HANYA membuat data yang benar-benar diperlukan
	// agar sistem bisa berjalan. TIDAK ada data dummy game/nominal/harga.
	// Data produk & harga harus ditarik dari Digiflazz via Admin Panel.
	// ================================================================

	// 1. Seed Superadmin User (hanya jika belum ada admin sama sekali)
	var adminCount int64
	db.Model(&domain.User{}).Where("role IN ?", []string{string(domain.RoleAdmin), string(domain.RoleSuperAdmin)}).Count(&adminCount)
	if adminCount == 0 {
		hashedPassword, _ := crypto.HashPassword("admin123")
		adminUser := domain.User{
			Name:        "Super Admin",
			Email:       "admin@topup.com",
			Password:    hashedPassword,
			PhoneNumber: "081234567890",
			Role:        domain.RoleSuperAdmin,
			Tier:        domain.TierVIP,
			Balance:     0, // Saldo 0, bukan dummy
			IsActive:    true,
		}
		db.Create(&adminUser)

		db.Create(&domain.AdminProfile{
			UserID:   adminUser.ID,
			FullName: "Super Administrator",
		})

		log.Println("[Seeder] Created default SuperAdmin (admin@topup.com / admin123)")
		log.Println("[Seeder] ⚠️  SEGERA ganti password admin melalui Admin Panel!")
	}

	// 2. Seed Digiflazz Provider config (hanya jika belum ada provider)
	//    Username & API Key HARUS diisi manual di .env atau Admin Panel
	var providerCount int64
	db.Model(&domain.Provider{}).Where("code = ?", "DIGIFLAZZ").Count(&providerCount)
	if providerCount == 0 {
		db.Create(&domain.Provider{
			Name:     "Digiflazz",
			Code:     "DIGIFLAZZ",
			BaseURL:  "https://api.digiflazz.com/v1",
			Username: "", // Diisi dari .env saat runtime
			APIKey:   "", // Diisi dari .env saat runtime
			Balance:  0,
			IsActive: true,
		})
		log.Println("[Seeder] Created Digiflazz Provider config (credentials loaded from .env)")
	}

	// 3. Seed Payment Method "Saldo Akun" saja (yang wajib ada untuk sistem)
	//    Payment method lainnya (QRIS, VA, dll) harus ditambah manual via Admin Panel
	var saldoMethodCount int64
	db.Model(&domain.PaymentMethod{}).Where("code = ?", "SALDO").Count(&saldoMethodCount)
	if saldoMethodCount == 0 {
		db.Create(&domain.PaymentMethod{
			Code:         "SALDO",
			Name:         "Saldo Akun Member",
			Category:     domain.PaymentCatBalance,
			FixedFee:     0,
			PercentFee:   0,
			MinAmount:    1000,
			MaxAmount:    10000000,
			ImageURL:     "/images/payments/wallet.png",
			Instructions: "Pembayaran langsung memotong saldo akun Anda secara instan.",
			IsActive:     true,
			SortOrder:    1,
		})
		log.Println("[Seeder] Created 'Saldo Akun' payment method")
	}

	// 4. Seed Global IP Whitelist Localhost (untuk development)
	var globalIpCount int64
	db.Model(&domain.IPWhitelist{}).Where("user_id IS NULL AND ip_address = ?", "127.0.0.1").Count(&globalIpCount)
	if globalIpCount == 0 {
		db.Create(&domain.IPWhitelist{
			IPAddress: "127.0.0.1",
			Label:     "Default Localhost IPv4",
			IsActive:  true,
			CreatedBy: "system",
		})
		db.Create(&domain.IPWhitelist{
			IPAddress: "::1",
			Label:     "Default Localhost IPv6",
			IsActive:  true,
			CreatedBy: "system",
		})
		log.Println("[Seeder] Created localhost IP whitelist (127.0.0.1, ::1)")
	}

	// ================================================================
	// ❌ TIDAK ADA data dummy game, nominal, harga, user reseller palsu
	// ✅ Data produk & harga real ditarik dari Digiflazz via Admin Panel
	//    (Menu: Digiflazz Center → Sinkronisasi Produk)
	// ================================================================

	_ = time.Now()
	_ = utils.GenerateAPIKey // Keep import
}
