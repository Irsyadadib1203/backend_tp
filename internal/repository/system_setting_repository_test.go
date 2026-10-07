package repository

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

func TestSystemSettingRepositoryGetByKey(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.SystemSetting{Key: "transaction_reference_format", Value: `{"ref_id_template":"REF-{time}"}`}).Error; err != nil {
		t.Fatal(err)
	}

	setting, err := NewSystemSettingRepository(db).Get("transaction_reference_format")
	if err != nil {
		t.Fatalf("get setting: %v", err)
	}
	if setting.Value != `{"ref_id_template":"REF-{time}"}` {
		t.Fatalf("value=%q", setting.Value)
	}
}
