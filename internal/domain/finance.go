package domain

import "time"

// ProviderFinanceBreakdown merepresentasikan rekapan penggunaan saldo dan performa satu provider
type ProviderFinanceBreakdown struct {
	ProviderID          uint       `json:"provider_id"`
	ProviderName        string     `json:"provider_name"`
	ProviderCode        string     `json:"provider_code"`
	IsActive            bool       `json:"is_active"`
	Balance             float64    `json:"balance"`
	BalanceUnit         string     `json:"balance_unit"`
	BalanceCheckedAt    *time.Time `json:"balance_checked_at"`
	TotalSpent          float64    `json:"total_spent"`          // Total saldo terpakai (modal transaksi sukses)
	TotalTransactions   int64      `json:"total_transactions"`   // Total transaksi hari ini
	SuccessTransactions int64      `json:"success_transactions"` // Transaksi sukses hari ini
	FailedTransactions  int64      `json:"failed_transactions"`  // Transaksi gagal/refunded hari ini
	TotalRevenue        float64    `json:"total_revenue"`        // Total omzet harga jual transaksi sukses
	TotalProfit         float64    `json:"total_profit"`         // Total estimasi keuntungan transaksi sukses
	Percentage          float64    `json:"percentage"`           // Persentase penggunaan terhadap total penggunaan semua provider hari ini
}

// DailyFinanceSummary merepresentasikan rekapan harian keuangan dan transaksi
type DailyFinanceSummary struct {
	Date                string                     `json:"date"`
	TotalSpent          float64                    `json:"total_spent"`          // Total saldo modal provider terpakai hari ini
	TotalTransactions   int64                      `json:"total_transactions"`   // Total jumlah transaksi hari ini
	SuccessTransactions int64                      `json:"success_transactions"` // Transaksi sukses
	PendingTransactions int64                      `json:"pending_transactions"` // Transaksi pending / processing
	FailedTransactions  int64                      `json:"failed_transactions"`  // Transaksi gagal / refunded
	TotalRevenue        float64                    `json:"total_revenue"`        // Total omzet / penjualan
	TotalProfit         float64                    `json:"total_profit"`         // Total estimasi keuntungan / profit
	Providers           []ProviderFinanceBreakdown `json:"providers"`            // Detail penggunaan saldo per provider
	Transactions        []Transaction              `json:"transactions"`         // Daftar transaksi terbaru pada tanggal tersebut
}
