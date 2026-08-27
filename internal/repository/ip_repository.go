package repository

import (
	"time"

	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type IPRepository interface {
	CreateWhitelist(ip *domain.IPWhitelist) error
	FindWhitelistByID(id uint) (*domain.IPWhitelist, error)
	ListWhitelists(offset, limit int, userID *uint) ([]domain.IPWhitelist, int64, error)
	DeleteWhitelist(id uint) error
	IsIPAllowed(ipAddress string, userID *uint) (bool, error)
	
	// Watchlist & Security logs
	LogAccess(log *domain.IPAccessLog) error
	ListAccessLogs(offset, limit int, status, ip string) ([]domain.IPAccessLog, int64, error)
	GetWatchlistByIP(ip string) (*domain.IPWatchlist, error)
	RecordFailedAttempt(ip, reason string) error
	BlockIP(ip, reason string, duration time.Duration) error
	UnblockIP(ip string) error
	ListWatchlist(offset, limit int) ([]domain.IPWatchlist, int64, error)
}

type ipRepository struct {
	db *gorm.DB
}

func NewIPRepository(db *gorm.DB) IPRepository {
	return &ipRepository{db: db}
}

func (r *ipRepository) CreateWhitelist(ip *domain.IPWhitelist) error {
	return r.db.Create(ip).Error
}

func (r *ipRepository) FindWhitelistByID(id uint) (*domain.IPWhitelist, error) {
	var item domain.IPWhitelist
	err := r.db.Preload("User").First(&item, id).Error
	return &item, err
}

func (r *ipRepository) ListWhitelists(offset, limit int, userID *uint) ([]domain.IPWhitelist, int64, error) {
	var items []domain.IPWhitelist
	var total int64
	query := r.db.Model(&domain.IPWhitelist{})
	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Preload("User").Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *ipRepository) DeleteWhitelist(id uint) error {
	return r.db.Delete(&domain.IPWhitelist{}, id).Error
}

func (r *ipRepository) IsIPAllowed(ipAddress string, userID *uint) (bool, error) {
	// First check if IP is in blocked watchlist
	var watchlist domain.IPWatchlist
	if err := r.db.Where("ip_address = ? AND is_blocked = ?", ipAddress, true).First(&watchlist).Error; err == nil {
		if watchlist.BlockedUntil == nil || watchlist.BlockedUntil.After(time.Now()) {
			return false, nil // Currently blocked
		}
	}

	// Check if IP is in whitelist
	var count int64
	query := r.db.Model(&domain.IPWhitelist{}).Where("ip_address = ? AND is_active = ?", ipAddress, true)
	if userID != nil {
		query = query.Where("user_id = ? OR user_id IS NULL", *userID)
	}

	err := query.Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *ipRepository) LogAccess(log *domain.IPAccessLog) error {
	return r.db.Create(log).Error
}

func (r *ipRepository) ListAccessLogs(offset, limit int, status, ip string) ([]domain.IPAccessLog, int64, error) {
	var logs []domain.IPAccessLog
	var total int64
	query := r.db.Model(&domain.IPAccessLog{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if ip != "" {
		query = query.Where("ip_address LIKE ?", "%"+ip+"%")
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&logs).Error
	return logs, total, err
}

func (r *ipRepository) GetWatchlistByIP(ip string) (*domain.IPWatchlist, error) {
	var item domain.IPWatchlist
	err := r.db.Where("ip_address = ?", ip).First(&item).Error
	return &item, err
}

func (r *ipRepository) RecordFailedAttempt(ip, reason string) error {
	var item domain.IPWatchlist
	err := r.db.Where("ip_address = ?", ip).First(&item).Error
	now := time.Now()
	if err != nil {
		item = domain.IPWatchlist{
			IPAddress:      ip,
			FailedAttempts: 1,
			LastAttemptAt:  now,
			BlockReason:    reason,
		}
		return r.db.Create(&item).Error
	}

	item.FailedAttempts++
	item.LastAttemptAt = now
	item.BlockReason = reason

	// Auto-block if failed attempts exceed 10 in quick succession
	if item.FailedAttempts >= 10 && !item.IsBlocked {
		item.IsBlocked = true
		blockedUntil := now.Add(24 * time.Hour)
		item.BlockedUntil = &blockedUntil
	}
	return r.db.Save(&item).Error
}

func (r *ipRepository) BlockIP(ip, reason string, duration time.Duration) error {
	var item domain.IPWatchlist
	now := time.Now()
	var blockedUntil *time.Time
	if duration > 0 {
		t := now.Add(duration)
		blockedUntil = &t
	}

	err := r.db.Where("ip_address = ?", ip).First(&item).Error
	if err != nil {
		item = domain.IPWatchlist{
			IPAddress:      ip,
			FailedAttempts: 1,
			LastAttemptAt:  now,
			IsBlocked:      true,
			BlockReason:    reason,
			BlockedUntil:   blockedUntil,
		}
		return r.db.Create(&item).Error
	}

	item.IsBlocked = true
	item.BlockReason = reason
	item.BlockedUntil = blockedUntil
	return r.db.Save(&item).Error
}

func (r *ipRepository) UnblockIP(ip string) error {
	return r.db.Model(&domain.IPWatchlist{}).Where("ip_address = ?", ip).Updates(map[string]interface{}{
		"is_blocked":      false,
		"failed_attempts": 0,
		"blocked_until":   nil,
	}).Error
}

func (r *ipRepository) ListWatchlist(offset, limit int) ([]domain.IPWatchlist, int64, error) {
	var items []domain.IPWatchlist
	var total int64
	if err := r.db.Model(&domain.IPWatchlist{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := r.db.Order("last_attempt_at DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}
