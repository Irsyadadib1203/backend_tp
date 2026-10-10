package repository

import (
	"time"
	"errors"
	"gorm.io/gorm"

	"topup-backend/internal/domain"
)

type TransactionRepository interface {
	Create(tx *domain.Transaction) error
	FindByID(id uint) (*domain.Transaction, error)
	FindByInvoiceNumber(invoice string) (*domain.Transaction, error)
	FindByRefID(refID string) (*domain.Transaction, error)
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
	ListAdmin(offset, limit int, status, search, startDate, endDate string) ([]domain.Transaction, int64, error)
	GetDashboardStats() (map[string]interface{}, error)
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

func (r *transactionRepository) FindByRefID(refID string) (*domain.Transaction, error) {
	var tx domain.Transaction
	err := r.db.Preload("Game").Preload("Nominal").Preload("Provider").Preload("User").
		Where("ref_id = ?", refID).First(&tx).Error
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

    err := r.db.
        Where("ref_id = ? AND user_id = ?", refID, userID).
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
	var txs []domain.Transaction
	err := r.db.Preload("Nominal").Preload("Provider").
		Where("status = ? AND provider_id = ?", domain.StatusProcessing, providerID).
		Where("provider_order_id IS NULL OR provider_order_id = '' OR provider_order_id = '-' ").
		Where("LOWER(provider_status) LIKE ? OR LOWER(provider_message) LIKE ? OR LOWER(provider_message) LIKE ?", "%saldo%", "%saldo%", "%balance%").
		Order("updated_at ASC").Limit(batchSize).Find(&txs).Error
	return txs, err
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

func (r *transactionRepository) ListAdmin(offset, limit int, status, search, startDate, endDate string) ([]domain.Transaction, int64, error) {
	var txs []domain.Transaction
	var total int64

	query := r.db.Model(&domain.Transaction{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if search != "" {
		query = query.Where("invoice_number LIKE ? OR customer_id LIKE ? OR customer_phone LIKE ?", "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}
	if startDate != "" && endDate != "" {
		query = query.Where("created_at BETWEEN ? AND ?", startDate, endDate)
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
