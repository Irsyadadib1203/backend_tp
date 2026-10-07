package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"topup-backend/internal/domain"
)

type SystemSettingRepository interface {
	Get(key string) (*domain.SystemSetting, error)
	Set(key, value string) error
}

type systemSettingRepository struct{ db *gorm.DB }

func NewSystemSettingRepository(db *gorm.DB) SystemSettingRepository {
	return &systemSettingRepository{db: db}
}

func (r *systemSettingRepository) Get(key string) (*domain.SystemSetting, error) {
	var setting domain.SystemSetting
	// `key` is a MySQL keyword. Use a structured column expression so GORM
	// quotes it for the active database dialect instead of emitting raw SQL.
	if err := r.db.Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).First(&setting).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}

func (r *systemSettingRepository) Set(key, value string) error {
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&domain.SystemSetting{Key: key, Value: value}).Error
}
