package domain

import (
	"time"

	"gorm.io/gorm"
)

type GameCategory string

const (
	CategoryGames        GameCategory = "Games"
	CategoryVoucher      GameCategory = "Voucher"
	CategoryEntertainment GameCategory = "Entertainment"
	CategoryPulsa        GameCategory = "Pulsa & PLN"
)

type Game struct {
	ID                 uint           `gorm:"primaryKey" json:"id"`
	Name               string         `gorm:"size:100;not null" json:"name"`
	Slug               string         `gorm:"size:120;uniqueIndex;not null" json:"slug"`
	Category           GameCategory   `gorm:"size:50;default:'Games'" json:"category"`
	Publisher          string         `gorm:"size:100" json:"publisher"`
	Description        string         `gorm:"type:text" json:"description"`
	ImageURL           string         `gorm:"size:255" json:"image_url"`
	BannerURL          string         `gorm:"size:255" json:"banner_url"`
	IsActive           bool           `gorm:"default:true" json:"is_active"`
	IsPopular          bool           `gorm:"default:false" json:"is_popular"`
	SortOrder          int            `gorm:"default:0" json:"sort_order"`
	
	// Validation config
	HasZoneID          bool           `gorm:"default:false" json:"has_zone_id"`
	UserIDLabel        string         `gorm:"size:50;default:'User ID'" json:"user_id_label"`
	ZoneIDLabel        string         `gorm:"size:50;default:'Zone ID / Server'" json:"zone_id_label"`
	NicknameCheckCode  string         `gorm:"size:50" json:"nickname_check_code"` // e.g. "MOBILE_LEGENDS", "FREE_FIRE"

	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	Nominals           []Nominal      `gorm:"foreignKey:GameID" json:"nominals,omitempty"`
	Providers          []GameProvider `gorm:"foreignKey:GameID" json:"game_providers,omitempty"`
}

type GameProvider struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	GameID             uint      `gorm:"index;not null" json:"game_id"`
	ProviderCode       string    `gorm:"size:50;not null" json:"provider_code"` // "DIGIFLAZZ", etc.
	ProviderCategoryID string    `gorm:"size:100" json:"provider_category_id"`  // Brand or Category code in provider
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
