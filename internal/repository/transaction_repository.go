package repository

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type TransactionRepository interface {
	Create(tx *domain.Transaction) error
	FindByID(id uint) (*domain.Transaction, error)
	FindByInvoiceNumber(invoice string) (*domain.Transaction, error)
	FindByRefID(refID string) (*domain.Transaction, error)
	FindByProviderOrderID(providerOrderID string) (*domain.Transaction, error)
	FindByProviderRefID(providerRefID string) (*domain.Transaction, error)
	FindByIdempotencyKey(key string) (*domain.Transaction, error)
	FindByRefIDAndUserID(refID string,userID uint,) (*domain.Transaction, error)
	Update(tx *domain.Transaction) error
	UpdateStatus(id uint, newStatus domain.TransactionStatus, reason string) error
	// MarkAsProcessingIfPending atomically transitions a transaction from
	// Pending to Processing. It returns (true, nil) only if this call was the
	// one that performed the transition, so concurrent/duplicate webhook
	// callbacks can never both proceed to fulfillment.
	MarkAsProcessingIfPending(id uint, paymentRef string, verifiedAt time.Time) (bool, error)
	// FindProcessingOlderThan returns at most batchSize processing transactions
	// whose updated_at (or created_at) is older than minAge, ordered by age
	// ascending (oldest first). Used exclusively by the reconciler worker.
	FindProcessingOlderThan(minAge time.Duration, batchSize int) ([]domain.Transaction, error)
	// ClaimProcessingForReconciliation atomically reserves a stale processing
	// transaction for one reconciliation attempt. The reservation advances
	// updated_at only when the transaction is still processing and stale, so
	// concurrent schedulers do not issue duplicate status checks.
	ClaimProcessingForReconciliation(id uint, staleBefore time.Time) (bool, error)
	// FindProcessingBalanceHolds returns retry-safe transactions that stopped
	// because the selected provider reported insufficient balance and has not
	// returned a provider order ID.
	FindProcessingBalanceHolds(providerID uint, batchSize int) ([]domain.Transaction, error)
	ListRecent(limit int) ([]domain.Transaction, error)
	ListByUser(userID uint, offset, limit int) ([]domain.Transaction, int64, error)
	ListAdmin(offset, limit int, status, search, startDate, endDate string, providerID uint, providerStatus string) ([]domain.Transaction, int64, error)
	GetDashboardStats() (map[string]interface{}, error)
	GetDailyProviderStats(startDate, endDate string) ([]ProviderStatRow, error)
	GetDailyTotals(startDate, endDate string) (totalSpent, totalRevenue, totalProfit float64, totalOrders, successOrders, pendingOrders, failedOrders int64, err error)
	ListTransactionsByDate(startDate, endDate string, limit int) ([]domain.Transaction, error)
}

type ProviderStatRow struct {
	ProviderID    uint    `gorm:"column:provider_id"`
	TotalOrders   int64   `gorm:"column:total_orders"`
	SuccessOrders int64   `gorm:"column:success_orders"`
	FailedOrders  int64   `gorm:"column:failed_orders"`
	TotalSpent    float64 `gorm:"column:total_spent"`
	TotalRevenue  float64 `gorm:"column:total_revenue"`
	TotalProfit   float64 `gorm:"column:total_profit"`
}

type transactionRepository struct {
	db *gorm.DB
}

func NewTransactionRepository(db *gorm.DB) TransactionRepository {
	return &transactionRepository{db: db}
}

func (r *transactionRepository) Create(tx *domain.Transaction) error {
	return r.db.Create(tx).Error
}

func (r *transactionRepository) FindByID(id uint) (*domain.Transaction, error) {
	var tx domain.Transaction
	err := r.db.Preload("Game").Preload("Nominal").Preload("Provider").Preload("User").Preload("StatusHistories").
		First(&tx, id).Error
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *transactionRepository) FindByInvoiceNumber(invoice string) (*domain.Transaction, error) {
	var tx domain.Transaction
	err := r.db.Preload("Game").Preload("Nominal").Preload("Provider").Preload("User").Preload("StatusHistories").
		Where("invoice_number = ?", invoice).First(&tx).Error
	if err != nil {
		return nil, err
	}
	return &tx, nil
}
func (r *transactionRepository) FindByProviderOrderID(
    providerOrderID string,
) (*domain.Transaction, error) {
    var tx domain.Transaction

    err := r.db.
        Where("provider_order_id = ?", providerOrderID).
        First(&tx).Error

    if err != nil {
        return nil, err
    }

    return &tx, nil
}

func (r *transactionRepository) FindByProviderRefID(providerRefID string) (*domain.Transaction, error) {
	var tx domain.Transaction
	err := r.db.Preload("Game").Preload("Nominal").Preload("Provider").Preload("User").
		Where("provider_ref_id = ?", providerRefID).First(&tx).Error
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *transactionRepository) FindByRefID(refID string) (*domain.Transaction, error) {
	var tx domain.Transaction
	err := r.db.Preload("Game").Preload("Nominal").Preload("Provider").Preload("User").
		Where("ref_id = ? OR provider_ref_id = ? OR provider_order_id = ?", refID, refID, refID).First(&tx).Error
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *transactionRepository) FindByIdempotencyKey(key string) (*domain.Transaction, error) {
	var tx domain.Transaction
	err := r.db.Where("idempotency_key = ?", key).First(&tx).Error
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (r *transactionRepository) FindByRefIDAndUserID(
    refID string,
    userID uint,
) (*domain.Transaction, error) {
    var tx domain.Transaction

    idemKey := fmt.Sprintf("h2h_%d_%s", userID, refID)
    err := r.db.
        Where("user_id = ? AND (ref_id = ? OR provider_order_id = ? OR idempotency_key = ?)", userID, refID, refID, idemKey).
        First(&tx).Error

    if err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
            return nil, nil
        }
        return nil, err
    }

    return &tx, nil
}


func (r *transactionRepository) Update(tx *domain.Transaction) error {
	return r.db.Save(tx).Error
}

// MarkAsProcessingIfPending performs an atomic conditional UPDATE
// (WHERE id = ? AND status = 'pending'). Only the caller whose UPDATE
// actually matched a row gets ok=true — this is what makes duplicate/
// concurrent webhook callbacks for the same transaction safe.
func (r *transactionRepository) MarkAsProcessingIfPending(id uint, paymentRef string, verifiedAt time.Time) (bool, error) {
	result := r.db.Model(&domain.Transaction{}).
		Where("id = ? AND status = ?", id, domain.StatusPending).
		Updates(map[string]interface{}{
			"status":              domain.StatusProcessing,
			"payment_reference":   paymentRef,
			"payment_verified_at": verifiedAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// FindProcessingOlderThan returns at most batchSize processing transactions whose
// updated_at is before (now - minAge), ordered oldest first. The reconciler uses
// this to find stale transactions that may have been missed by push callbacks.
func (r *transactionRepository) FindProcessingOlderThan(minAge time.Duration, batchSize int) ([]domain.Transaction, error) {
	var txs []domain.Transaction
	cutoff := time.Now().Add(-minAge)
	err := r.db.Preload("Nominal").Preload("Provider").
		Where("status = ? AND updated_at < ?", domain.StatusProcessing, cutoff).
		Order("updated_at ASC").
		Limit(batchSize).
		Find(&txs).Error
	return txs, err
}

// ClaimProcessingForReconciliation advances updated_at only for a transaction
// that is still processing and stale. It deliberately does not change the
// business status or hold a database lock while the provider is called.
func (r *transactionRepository) ClaimProcessingForReconciliation(id uint, staleBefore time.Time) (bool, error) {
	result := r.db.Model(&domain.Transaction{}).
		Where("id = ? AND status = ? AND updated_at < ?", id, domain.StatusProcessing, staleBefore).
		Update("updated_at", time.Now())
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *transactionRepository) FindProcessingBalanceHolds(providerID uint, batchSize int) ([]domain.Transaction, error) {
	var candidates []domain.Transaction
	err := r.db.Preload("Nominal").Preload("Provider").
		Where("status = ? AND provider_id = ?", domain.StatusProcessing, providerID).
		Where("LOWER(provider_status) LIKE ? OR LOWER(provider_message) LIKE ? OR LOWER(provider_message) LIKE ?", "%saldo%", "%saldo%", "%balance%").
		Order("updated_at ASC").Limit(batchSize * 5).Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	// "belum ada order di provider" berarti ProviderOrderID kosong
	// (ProviderOrderID kini hanya diisi dari respons provider, bukan dari ref internal).
	txs := make([]domain.Transaction, 0, batchSize)
	for _, tx := range candidates {
		id := strings.TrimSpace(tx.ProviderOrderID)
		if id == "" || id == "-" {
			txs = append(txs, tx)
			if len(txs) >= batchSize {
				break
			}
		}
	}
	return txs, nil
}

func (r *transactionRepository) UpdateStatus(id uint, newStatus domain.TransactionStatus, reason string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var transaction domain.Transaction
		if err := tx.First(&transaction, id).Error; err != nil {
			return err
		}

		oldStatus := transaction.Status
		transaction.Status = newStatus

		if newStatus == domain.StatusSuccess || newStatus == domain.StatusFailed {
			now := time.Now()
			transaction.CompletedAt = &now
		}

		if err := tx.Save(&transaction).Error; err != nil {
			return err
		}

		history := domain.TransactionStatusHistory{
			TransactionID: id,
			FromStatus:    oldStatus,
			ToStatus:      newStatus,
			Reason:        reason,
		}
		return tx.Create(&history).Error
	})
}

func (r *transactionRepository) ListRecent(limit int) ([]domain.Transaction, error) {
	var txs []domain.Transaction
	err := r.db.Preload("Game").Preload("Nominal").
		Where("status = ?", domain.StatusSuccess).
		Order("created_at DESC").
		Limit(limit).
		Find(&txs).Error
	return txs, err
}

func (r *transactionRepository) ListByUser(userID uint, offset, limit int) ([]domain.Transaction, int64, error) {
	var txs []domain.Transaction
	var total int64

	query := r.db.Model(&domain.Transaction{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Preload("Game").Preload("Nominal").
		Order("id DESC").
		Offset(offset).Limit(limit).
		Find(&txs).Error
	return txs, total, err
}

func (r *transactionRepository) ListAdmin(offset, limit int, status, search, startDate, endDate string, providerID uint, providerStatus string) ([]domain.Transaction, int64, error) {
	var txs []domain.Transaction
	var total int64

	query := r.db.Model(&domain.Transaction{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if search != "" {
		like := "%" + search + "%"
		query = query.Where("(invoice_number LIKE ? OR customer_id LIKE ? OR customer_phone LIKE ?)", like, like, like)
	}
	if startDate != "" {
		query = query.Where("created_at >= ?", startDate+" 00:00:00")
	}
	if endDate != "" {
		query = query.Where("created_at <= ?", endDate+" 23:59:59")
	}
	if providerID > 0 {
		query = query.Where("provider_id = ?", providerID)
	}
	switch strings.ToLower(strings.TrimSpace(providerStatus)) {
	case "sukses":
		query = query.Where("(LOWER(provider_status) LIKE ? OR LOWER(provider_status) LIKE ?)", "%sukses%", "%success%")
	case "gagal":
		query = query.Where("(LOWER(provider_status) LIKE ? OR LOWER(provider_status) LIKE ? OR LOWER(provider_status) LIKE ?)", "%gagal%", "%fail%", "%error%")
	case "pending":
		query = query.Where("LOWER(provider_status) LIKE ?", "%pending%")
	case "processing":
		query = query.Where("LOWER(provider_status) LIKE ?", "%process%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Preload("Game").Preload("Nominal").Preload("User").Preload("Provider").
		Order("id DESC").
		Offset(offset).Limit(limit).
		Find(&txs).Error
	return txs, total, err
}

func (r *transactionRepository) GetDashboardStats() (map[string]interface{}, error) {
	var totalRevenue, totalProfit float64
	var totalOrders, successOrders, pendingOrders, failedOrders int64

	r.db.Model(&domain.Transaction{}).Count(&totalOrders)
	r.db.Model(&domain.Transaction{}).Where("status = ?", domain.StatusSuccess).Count(&successOrders)
	r.db.Model(&domain.Transaction{}).Where("status = ? OR status = ?", domain.StatusPending, domain.StatusProcessing).Count(&pendingOrders)
	r.db.Model(&domain.Transaction{}).Where("status = ?", domain.StatusFailed).Count(&failedOrders)

	r.db.Model(&domain.Transaction{}).Where("status = ?", domain.StatusSuccess).Select("COALESCE(SUM(total_amount), 0)").Scan(&totalRevenue)
	r.db.Model(&domain.Transaction{}).Where("status = ?", domain.StatusSuccess).Select("COALESCE(SUM(profit), 0)").Scan(&totalProfit)

	// Today stats
	todayStart := time.Now().Format("2006-01-02 00:00:00")
	var todayRevenue, todayProfit float64
	var todayOrders int64
	r.db.Model(&domain.Transaction{}).Where("created_at >= ?", todayStart).Count(&todayOrders)
	r.db.Model(&domain.Transaction{}).Where("status = ? AND created_at >= ?", domain.StatusSuccess, todayStart).Select("COALESCE(SUM(total_amount), 0)").Scan(&todayRevenue)
	r.db.Model(&domain.Transaction{}).Where("status = ? AND created_at >= ?", domain.StatusSuccess, todayStart).Select("COALESCE(SUM(profit), 0)").Scan(&todayProfit)

	return map[string]interface{}{
		"total_orders":   totalOrders,
		"success_orders": successOrders,
		"pending_orders": pendingOrders,
		"failed_orders":  failedOrders,
		"total_revenue":  totalRevenue,
		"total_profit":   totalProfit,
		"today_orders":   todayOrders,
		"today_revenue":  todayRevenue,
		"today_profit":   todayProfit,
	}, nil
}

func (r *transactionRepository) GetDailyProviderStats(startDate, endDate string) ([]ProviderStatRow, error) {
	var rows []ProviderStatRow
	err := r.db.Model(&domain.Transaction{}).
		Select(`provider_id,
			COUNT(*) as total_orders,
			SUM(CASE WHEN status = '` + string(domain.StatusSuccess) + `' THEN 1 ELSE 0 END) as success_orders,
			SUM(CASE WHEN status IN ('` + string(domain.StatusFailed) + `', '` + string(domain.StatusRefunded) + `') THEN 1 ELSE 0 END) as failed_orders,
			COALESCE(SUM(CASE WHEN status = '` + string(domain.StatusSuccess) + `' THEN base_price ELSE 0 END), 0) as total_spent,
			COALESCE(SUM(CASE WHEN status = '` + string(domain.StatusSuccess) + `' THEN total_amount ELSE 0 END), 0) as total_revenue,
			COALESCE(SUM(CASE WHEN status = '` + string(domain.StatusSuccess) + `' THEN profit ELSE 0 END), 0) as total_profit`).
		Where("created_at >= ? AND created_at <= ?", startDate, endDate).
		Group("provider_id").
		Scan(&rows).Error
	return rows, err
}

func (r *transactionRepository) GetDailyTotals(startDate, endDate string) (totalSpent, totalRevenue, totalProfit float64, totalOrders, successOrders, pendingOrders, failedOrders int64, err error) {
	if countErr := r.db.Model(&domain.Transaction{}).Where("created_at >= ? AND created_at <= ?", startDate, endDate).Count(&totalOrders).Error; countErr != nil {
		err = countErr
		return
	}
	r.db.Model(&domain.Transaction{}).Where("status = ? AND created_at >= ? AND created_at <= ?", domain.StatusSuccess, startDate, endDate).Count(&successOrders)
	r.db.Model(&domain.Transaction{}).Where("status IN (?, ?) AND created_at >= ? AND created_at <= ?", domain.StatusPending, domain.StatusProcessing, startDate, endDate).Count(&pendingOrders)
	r.db.Model(&domain.Transaction{}).Where("status IN (?, ?) AND created_at >= ? AND created_at <= ?", domain.StatusFailed, domain.StatusRefunded, startDate, endDate).Count(&failedOrders)

	r.db.Model(&domain.Transaction{}).Where("status = ? AND created_at >= ? AND created_at <= ?", domain.StatusSuccess, startDate, endDate).Select("COALESCE(SUM(base_price), 0)").Scan(&totalSpent)
	r.db.Model(&domain.Transaction{}).Where("status = ? AND created_at >= ? AND created_at <= ?", domain.StatusSuccess, startDate, endDate).Select("COALESCE(SUM(total_amount), 0)").Scan(&totalRevenue)
	r.db.Model(&domain.Transaction{}).Where("status = ? AND created_at >= ? AND created_at <= ?", domain.StatusSuccess, startDate, endDate).Select("COALESCE(SUM(profit), 0)").Scan(&totalProfit)

	return
}

func (r *transactionRepository) ListTransactionsByDate(startDate, endDate string, limit int) ([]domain.Transaction, error) {
	var txs []domain.Transaction
	query := r.db.Model(&domain.Transaction{}).
		Preload("Game").Preload("Nominal").Preload("Provider").Preload("User").
		Where("created_at >= ? AND created_at <= ?", startDate, endDate).
		Order("id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&txs).Error
	return txs, err
}
