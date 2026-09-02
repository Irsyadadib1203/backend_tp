package domain

import "time"

// PermissionResource defines the accessible feature or module in the system
type PermissionResource string

const (
	ResourceDashboard      PermissionResource = "dashboard"
	ResourceTransactions   PermissionResource = "transactions"
	ResourceGames          PermissionResource = "games"
	ResourceNominals       PermissionResource = "nominals"
	ResourceBanners        PermissionResource = "banners"
	ResourceArticles       PermissionResource = "articles"
	ResourceDigiflazz      PermissionResource = "digiflazz"
	ResourceKiosgamer      PermissionResource = "kiosgamer"
	ResourceIPWhitelist    PermissionResource = "ip_whitelist"
	ResourceUsers          PermissionResource = "users"
	ResourceDeposits       PermissionResource = "deposits"
	ResourcePaymentMethods PermissionResource = "payment_methods"
	ResourceDocs           PermissionResource = "docs"
	ResourceSettings       PermissionResource = "settings"
	ResourceRoles          PermissionResource = "roles" // Superadmin only
)

// AllResources list of all standard module resources
var AllResources = []PermissionResource{
	ResourceDashboard,
	ResourceTransactions,
	ResourceGames,
	ResourceNominals,
	ResourceBanners,
	ResourceArticles,
	ResourceDigiflazz,
	ResourceKiosgamer,
	ResourceIPWhitelist,
	ResourceUsers,
	ResourceDeposits,
	ResourcePaymentMethods,
	ResourceDocs,
	ResourceSettings,
}

// RolePermission defines whether a role can access a specific system resource
type RolePermission struct {
	ID        uint               `gorm:"primaryKey" json:"id"`
	Role      Role               `gorm:"size:20;index;not null" json:"role"` // "admin" or "operator"
	Resource  PermissionResource `gorm:"size:50;index;not null" json:"resource"`
	Allowed   bool               `gorm:"default:true;not null" json:"allowed"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}
