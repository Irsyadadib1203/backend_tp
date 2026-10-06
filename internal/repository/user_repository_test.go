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
