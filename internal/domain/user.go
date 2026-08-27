package domain

import (
	"time"

	"gorm.io/gorm"
)

type Role string

const (
	RoleSuperAdmin Role = "superadmin"
	RoleAdmin      Role = "admin"
	RoleOperator   Role = "operator"
	RoleMember     Role = "member"
	RoleReseller   Role = "reseller"
)

type Tier string

const (
	TierPublic   Tier = "public"
	TierMember   Tier = "member"
	TierVIP      Tier = "vip"
	TierReseller Tier = "reseller"
)

type User struct {
	ID              uint           `gorm:"primaryKey" json:"id"`
	Name            string         `gorm:"size:100;not null" json:"name"`
	Email           string         `gorm:"size:100;uniqueIndex;not null" json:"email"`
	Password        string         `gorm:"size:255;not null" json:"-"`
	PhoneNumber     string         `gorm:"size:20" json:"phone_number"`
	Role            Role           `gorm:"size:20;default:'member'" json:"role"`
	Tier            Tier           `gorm:"size:20;default:'member'" json:"tier"`
	Balance         float64        `gorm:"type:decimal(15,2);default:0" json:"balance"`
	IsActive        bool           `gorm:"default:true" json:"is_active"`
	EmailVerifiedAt *time.Time     `json:"email_verified_at"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	APIKey       *APIKey           `gorm:"foreignKey:UserID" json:"api_key,omitempty"`
	IPWhitelists []IPWhitelist     `gorm:"foreignKey:UserID" json:"ip_whitelists,omitempty"`
	Mutations    []BalanceMutation `gorm:"foreignKey:UserID" json:"mutations,omitempty"`
}

type AdminProfile struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex;not null" json:"user_id"`
	FullName  string    `gorm:"size:100" json:"full_name"`
	AvatarURL string    `gorm:"size:255" json:"avatar_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
