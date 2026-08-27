package service

import (
	"errors"
	"net"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/repository"
)

type IPWhitelistService interface {
	ValidateIP(clientIP string, userID *uint) (bool, string)
	RecordAccess(clientIP, endpoint, method string, statusCode int, status, reason, userAgent, payload string)
	AddWhitelist(ipAddress, label string, userID *uint, createdBy string) (*domain.IPWhitelist, error)
	DeleteWhitelist(id uint) error
	ListWhitelists(offset, limit int, userID *uint) ([]domain.IPWhitelist, int64, error)
	BlockIP(ipAddress, reason string, duration time.Duration) error
	UnblockIP(ipAddress string) error
	ListWatchlist(offset, limit int) ([]domain.IPWatchlist, int64, error)
	ListAccessLogs(offset, limit int, status, ip string) ([]domain.IPAccessLog, int64, error)
}

type ipWhitelistService struct {
	ipRepo repository.IPRepository
}

func NewIPWhitelistService(ipRepo repository.IPRepository) IPWhitelistService {
	return &ipWhitelistService{ipRepo: ipRepo}
}

func (s *ipWhitelistService) ValidateIP(clientIP string, userID *uint) (bool, string) {
	// Normalize client IP (strip port if present)
	host, _, err := net.SplitHostPort(clientIP)
	if err == nil {
		clientIP = host
	}

	allowed, err := s.ipRepo.IsIPAllowed(clientIP, userID)
	if err != nil {
		return false, "database error"
	}

	if !allowed {
		_ = s.ipRepo.RecordFailedAttempt(clientIP, "Unauthorized IP access attempt")
		return false, "IP address is not whitelisted"
	}

	return true, ""
}

func (s *ipWhitelistService) RecordAccess(clientIP, endpoint, method string, statusCode int, status, reason, userAgent, payload string) {
	host, _, err := net.SplitHostPort(clientIP)
	if err == nil {
		clientIP = host
	}

	logEntry := &domain.IPAccessLog{
		IPAddress:  clientIP,
		Endpoint:   endpoint,
		Method:     method,
		StatusCode: statusCode,
		Status:     status,
		Reason:     reason,
		UserAgent:  userAgent,
		Payload:    payload,
		CreatedAt:  time.Now(),
	}

	_ = s.ipRepo.LogAccess(logEntry)
}

func (s *ipWhitelistService) AddWhitelist(ipAddress, label string, userID *uint, createdBy string) (*domain.IPWhitelist, error) {
	// Validate IP address format
	if parsed := net.ParseIP(ipAddress); parsed == nil {
		return nil, errors.New("invalid IP address format")
	}

	item := &domain.IPWhitelist{
		UserID:    userID,
		IPAddress: ipAddress,
		Label:     label,
		IsActive:  true,
		CreatedBy: createdBy,
	}

	if err := s.ipRepo.CreateWhitelist(item); err != nil {
		return nil, err
	}

	return item, nil
}

func (s *ipWhitelistService) DeleteWhitelist(id uint) error {
	return s.ipRepo.DeleteWhitelist(id)
}

func (s *ipWhitelistService) ListWhitelists(offset, limit int, userID *uint) ([]domain.IPWhitelist, int64, error) {
	return s.ipRepo.ListWhitelists(offset, limit, userID)
}

func (s *ipWhitelistService) BlockIP(ipAddress, reason string, duration time.Duration) error {
	return s.ipRepo.BlockIP(ipAddress, reason, duration)
}

func (s *ipWhitelistService) UnblockIP(ipAddress string) error {
	return s.ipRepo.UnblockIP(ipAddress)
}

func (s *ipWhitelistService) ListWatchlist(offset, limit int) ([]domain.IPWatchlist, int64, error) {
	return s.ipRepo.ListWatchlist(offset, limit)
}

func (s *ipWhitelistService) ListAccessLogs(offset, limit int, status, ip string) ([]domain.IPAccessLog, int64, error) {
	return s.ipRepo.ListAccessLogs(offset, limit, status, ip)
}
