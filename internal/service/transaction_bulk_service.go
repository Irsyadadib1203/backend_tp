package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/sse"
)

// BulkAction adalah aksi massal yang boleh dijalankan admin.
type BulkAction string

const (
	BulkActionRetry   BulkAction = "retry"   // rehit
	BulkActionSuccess BulkAction = "success" // set sukses manual (SN dibuat otomatis)
	BulkActionFail    BulkAction = "fail"    // set gagal manual (+ refund saldo bila bayar SALDO)
)

// MaxBulkTransactions membatasi jumlah transaksi per permintaan agar request tidak terlalu lama.
const MaxBulkTransactions = 100

type BulkActionResult struct {
	ID            uint   `json:"id"`
	InvoiceNumber string `json:"invoice_number,omitempty"`
	Success       bool   `json:"success"`
	Message       string `json:"message"`
}

// BulkTransactionService sengaja sempit dan TIDAK ditambahkan ke interface
// TransactionService, supaya fake/mock di file tes tidak perlu diubah.
type BulkTransactionService interface {
	BulkManualAction(action BulkAction, ids []uint, notes string) ([]BulkActionResult, error)
}

// BulkManualAction menjalankan satu aksi untuk banyak transaksi secara berurutan.
// Hanya transaksi berstatus processing yang diproses; yang lain dilewati dan
// dilaporkan di hasil. Setiap transaksi memakai method manual yang sudah ada
// (ManualRetry / ManualSetSuccess / ManualSetFailed), jadi aturannya sama
// dengan aksi satuan.
func (s *transactionService) BulkManualAction(action BulkAction, ids []uint, notes string) ([]BulkActionResult, error) {
	switch action {
	case BulkActionRetry, BulkActionSuccess, BulkActionFail:
	default:
		return nil, fmt.Errorf("aksi tidak dikenal: %q", action)
	}

	seen := make(map[uint]bool, len(ids))
	unique := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil, errors.New("tidak ada transaksi yang dipilih")
	}
	if len(unique) > MaxBulkTransactions {
		return nil, fmt.Errorf("maksimal %d transaksi per aksi massal", MaxBulkTransactions)
	}

	results := make([]BulkActionResult, 0, len(unique))
	for _, id := range unique {
		res := BulkActionResult{ID: id}

		tx, err := s.txRepo.FindByID(id)
		if err != nil || tx == nil {
			res.Message = "transaksi tidak ditemukan"
			results = append(results, res)
			continue
		}
		res.InvoiceNumber = tx.InvoiceNumber

		if tx.Status != domain.StatusProcessing {
			res.Message = fmt.Sprintf("dilewati: status %s (aksi massal hanya untuk processing)", tx.Status)
			results = append(results, res)
			continue
		}

		var actErr error
		switch action {
		case BulkActionRetry:
			actErr = s.ManualRetry(id)
			res.Message = "rehit dijadwalkan"
		case BulkActionSuccess:
			actErr = s.ManualSetSuccess(id, notes, "") // SN dibuat otomatis oleh buildManualSN
			res.Message = "diset sukses"
		case BulkActionFail:
			actErr = s.ManualSetFailed(id, notes)
			res.Message = "diset gagal"
		}

		if actErr != nil {
			res.Message = actErr.Error()
		} else {
			res.Success = true
		}
		results = append(results, res)
	}
	return results, nil
}

// ManualSetFailed menandai transaksi sebagai gagal oleh admin. Untuk pembayaran
// SALDO/SALDO_H2H saldo dikembalikan (idempotent). Pembayaran non-saldo (mis.
// Tripay) TIDAK dikembalikan otomatis; admin harus menanganinya manual.
func (s *transactionService) ManualSetFailed(transactionID uint, notes string) error {
	tx, err := s.txRepo.FindByID(transactionID)
	if err != nil || tx == nil {
		return errors.New("transaction not found")
	}

	switch tx.Status {
	case domain.StatusFailed, domain.StatusRefunded:
		return errors.New("transaksi sudah berstatus gagal/refund")
	case domain.StatusSuccess:
		return errors.New("transaksi sudah sukses, tidak bisa diubah menjadi gagal")
	}

	reason := strings.TrimSpace(notes)
	if reason == "" {
		reason = "Gagal manual oleh admin"
	}

	if err := s.safeRefundTransaction(tx, "Pengembalian dana: "+reason); err != nil {
		return fmt.Errorf("gagal refund saldo: %w", err)
	}

	now := time.Now()
	tx.Status = domain.StatusFailed
	tx.ProviderStatus = "Gagal (Manual)"
	tx.ProviderMessage = truncateManualText("Gagal manual: "+reason, 255)
	tx.CompletedAt = &now
	_ = s.txRepo.Update(tx)

	err = s.txRepo.UpdateStatus(transactionID, domain.StatusFailed, "Manual failed by admin: "+reason)
	sse.GlobalHub.Broadcast(tx.InvoiceNumber, "status_update", map[string]interface{}{
		"status": "failed", "invoice": tx.InvoiceNumber, "completed_at": now,
	})
	return err
}

// buildManualSN membuat SN otomatis untuk sukses manual, formatnya:
//
//	<nickname atau ID pelanggan>. RefId : <invoice_number>
//
// Ubah format di fungsi ini saja jika bentuknya berubah.
func buildManualSN(tx *domain.Transaction) string {
	target := strings.TrimSpace(tx.Nickname)
	if target == "" {
		target = strings.TrimSpace(tx.CustomerID)
	}
	return fmt.Sprintf("%s. RefId : %s", target, tx.InvoiceNumber)
}

func truncateManualText(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}