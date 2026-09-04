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

	// 2. Seed Digiflazz & Kiosgamer Provider configs (hanya jika belum ada)
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

	var kiosgamerCount int64
	db.Model(&domain.Provider{}).Where("code = ?", "KIOSGAMER").Count(&kiosgamerCount)
	if kiosgamerCount == 0 {
		db.Create(&domain.Provider{
			Name:     "Kiosgamer",
			Code:     "KIOSGAMER",
			BaseURL:  "https://kiosgamer.co.id/api",
			Username: "",
			APIKey:   "",
			Balance:  0,
			IsActive: true,
		})
		log.Println("[Seeder] Created Kiosgamer Provider config")
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

	// 3b. Seed Payment Methods populer sebagai fallback
	//     (akan di-update otomatis saat Tripay Sync dijalankan dari Admin Panel)
	fallbackMethods := []domain.PaymentMethod{
		{
			Code: "QRIS", Name: "QRIS (Semua Dompet Digital)",
			Category:     domain.PaymentCatQRIS,
			FixedFee:     0, PercentFee: 0.7,
			MinAmount: 1000, MaxAmount: 10000000,
			ImageURL:     "/images/payments/qris.png",
			Instructions: "Scan QR code menggunakan aplikasi dompet digital atau mobile banking Anda.",
			IsActive:     true, SortOrder: 2,
		},
		{
			Code: "GOPAY", Name: "GoPay",
			Category:     domain.PaymentCatEWallet,
			FixedFee:     0, PercentFee: 1.5,
			MinAmount: 1000, MaxAmount: 10000000,
			ImageURL:     "/images/payments/gopay.png",
			Instructions: "Pembayaran melalui aplikasi Gojek / GoPay.",
			IsActive:     true, SortOrder: 3,
		},
		{
			Code: "OVO", Name: "OVO",
			Category:     domain.PaymentCatEWallet,
			FixedFee:     0, PercentFee: 1.5,
			MinAmount: 1000, MaxAmount: 10000000,
			ImageURL:     "/images/payments/ovo.png",
			Instructions: "Pembayaran melalui aplikasi OVO.",
			IsActive:     true, SortOrder: 4,
		},
		{
			Code: "DANA", Name: "DANA",
			Category:     domain.PaymentCatEWallet,
			FixedFee:     0, PercentFee: 1.5,
			MinAmount: 1000, MaxAmount: 10000000,
			ImageURL:     "/images/payments/dana.png",
			Instructions: "Pembayaran melalui aplikasi DANA.",
			IsActive:     true, SortOrder: 5,
		},
		{
			Code: "SHOPEE_PAY", Name: "ShopeePay",
			Category:     domain.PaymentCatEWallet,
			FixedFee:     0, PercentFee: 1.5,
			MinAmount: 1000, MaxAmount: 10000000,
			ImageURL:     "/images/payments/shopeepay.png",
			Instructions: "Pembayaran melalui aplikasi Shopee.",
			IsActive:     true, SortOrder: 6,
		},
		{
			Code: "BCAVA", Name: "BCA Virtual Account",
			Category:     domain.PaymentCatVirtualAccount,
			FixedFee:     4000, PercentFee: 0,
			MinAmount: 10000, MaxAmount: 100000000,
			ImageURL:     "/images/payments/bca.png",
			Instructions: "Transfer ke nomor Virtual Account BCA yang tertera pada invoice.",
			IsActive:     true, SortOrder: 7,
		},
		{
			Code: "BRIVA", Name: "BRI Virtual Account",
			Category:     domain.PaymentCatVirtualAccount,
			FixedFee:     4000, PercentFee: 0,
			MinAmount: 10000, MaxAmount: 100000000,
			ImageURL:     "/images/payments/bri.png",
			Instructions: "Transfer ke nomor Virtual Account BRI yang tertera pada invoice.",
			IsActive:     true, SortOrder: 8,
		},
		{
			Code: "MANDIRIVA", Name: "Mandiri Virtual Account",
			Category:     domain.PaymentCatVirtualAccount,
			FixedFee:     4000, PercentFee: 0,
			MinAmount: 10000, MaxAmount: 100000000,
			ImageURL:     "/images/payments/mandiri.png",
			Instructions: "Bayar ke nomor Virtual Account Mandiri via ATM, m-Banking, atau internet banking.",
			IsActive:     true, SortOrder: 9,
		},
		{
			Code: "BNIVA", Name: "BNI Virtual Account",
			Category:     domain.PaymentCatVirtualAccount,
			FixedFee:     4000, PercentFee: 0,
			MinAmount: 10000, MaxAmount: 100000000,
			ImageURL:     "/images/payments/bni.png",
			Instructions: "Transfer ke nomor Virtual Account BNI yang tertera pada invoice.",
			IsActive:     true, SortOrder: 10,
		},
	}

	for _, pm := range fallbackMethods {
		var count int64
		db.Model(&domain.PaymentMethod{}).Where("code = ?", pm.Code).Count(&count)
		if count == 0 {
			if err := db.Create(&pm).Error; err != nil {
				log.Printf("[Seeder] Failed to create payment method %s: %v", pm.Code, err)
			} else {
				log.Printf("[Seeder] Created payment method: %s", pm.Name)
			}
		}
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
