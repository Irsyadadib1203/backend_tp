package database

import (
	"fmt"
	"log"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"time"

	"topup-backend/config"
	"topup-backend/internal/domain"
)

var DB *gorm.DB

func InitDB(cfg *config.Config) *gorm.DB {
	var dialector gorm.Dialector

	switch cfg.DBDriver {
	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName)
		dialector = mysql.Open(dsn)
	case "postgres":
		dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=Asia/Jakarta",
			cfg.DBHost, cfg.DBUser, cfg.DBPass, cfg.DBName, cfg.DBPort)
		dialector = postgres.Open(dsn)
	case "sqlite":
		fallthrough
	default:
		dialector = sqlite.Open(cfg.DBSQLite)
	}

	var gormLogger logger.Interface = logger.Default.LogMode(logger.Warn)
	if cfg.AppEnv == "development" {
		gormLogger = logger.Default.LogMode(logger.Info)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:      gormLogger,
		PrepareStmt: true, // Cache prepared statements for ultra-fast query execution
	})
	if err != nil {
		log.Fatalf("[Database] Failed to connect to database (%s): %v", cfg.DBDriver, err)
	}

	// -------------------------------------------------------------
	// Database Connection Pool Tuning (High Performance & Resilient)
	// -------------------------------------------------------------
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("[Database] Failed to get generic database object: %v", err)
	}

	if cfg.DBDriver != "sqlite" {
		sqlDB.SetMaxOpenConns(50)                  // Maximum active connections
		sqlDB.SetMaxIdleConns(25)                  // Maximum idle connections kept in pool
		sqlDB.SetConnMaxLifetime(10 * time.Minute) // Maximum amount of time a connection may be reused
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)  // Maximum amount of time a connection may be idle
	} else {
		// SQLite is single-file; limit concurrent write connections to avoid database locked errors
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}

	log.Printf("[Database] Connected successfully to %s database with connection pool configured", cfg.DBDriver)

	// Run Auto Migrations
	err = db.AutoMigrate(
		&domain.User{},
		&domain.AdminProfile{},
		&domain.Game{},
		&domain.GameProvider{},
		&domain.Nominal{},
		&domain.Provider{},
		&domain.Transaction{},
		&domain.TransactionStatusHistory{},
		&domain.DepositRequest{},
		&domain.BalanceMutation{},
		&domain.IPWhitelist{},
		&domain.IPAccessLog{},
		&domain.IPWatchlist{},
		&domain.APIKey{},
		&domain.PaymentMethod{},
		&domain.WebhookLog{},
		&domain.Banner{},
		&domain.Article{},
		&domain.KiosgamerCredential{},
		&domain.RolePermission{},
	)
	if err != nil {
		log.Fatalf("[Database] Migration error: %v", err)
	}

	DB = db
	return db
}
