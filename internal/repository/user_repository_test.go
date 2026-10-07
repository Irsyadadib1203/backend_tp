package repository

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

func TestUpdateBalanceRefundIsIdempotentAtRepositoryBoundary(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.User{}, &domain.BalanceMutation{}); err != nil {
		t.Fatal(err)
	}
	user := &domain.User{Name: "Refund Test", Email: "refund@example.test", Password: "not-used", Balance: 100}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}

	repo := NewUserRepository(db)
	for i := 0; i < 2; i++ {
		if err := repo.UpdateBalance(user.ID, 25, domain.MutationCredit, "REFUND", "INV-REFUND-1", "provider final failure"); err != nil {
			t.Fatalf("refund attempt %d: %v", i+1, err)
		}
	}

	var stored domain.User
	if err := db.First(&stored, user.ID).Error; err != nil {
		t.Fatalf("load refunded user: %v", err)
	}
	if stored.Balance != 125 {
		t.Fatalf("balance=%v, want 125", stored.Balance)
	}
	var mutations int64
	if err := db.Model(&domain.BalanceMutation{}).
		Where("user_id = ? AND reference_type = ? AND reference_id = ?", user.ID, "REFUND", "INV-REFUND-1").
		Count(&mutations).Error; err != nil || mutations != 1 {
		t.Fatalf("refund mutations=%d err=%v, want 1", mutations, err)
	}
}

func TestFindByAPIKey(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.User{}, &domain.APIKey{}, &domain.IPWhitelist{}); err != nil {
		t.Fatal(err)
	}
	user := &domain.User{Name: "API Key Test", Email: "api-key@example.test", Password: "not-used"}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.APIKey{UserID: user.ID, Key: "active-key", Secret: "secret", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}

	resolvedUser, apiKey, err := NewUserRepository(db).FindByAPIKey("active-key")
	if err != nil {
		t.Fatalf("find API key: %v", err)
	}
	if resolvedUser.ID != user.ID || apiKey.Key != "active-key" {
		t.Fatalf("unexpected API key resolution: user=%d key=%q", resolvedUser.ID, apiKey.Key)
	}
}
