package database

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"topup-backend/config"
	"topup-backend/internal/domain"
)

var DB *gorm.DB

func InitDB(cfg *config.Config) *gorm.DB {
	var dialector gorm.Dialector

	switch cfg.DBDriver {
	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Asia%%2FJakarta&time_zone=%%27%%2B07%%3A00%%27",
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

	// Logger: sembunyikan "record not found", tetap catat query lambat & error asli.
	logLevel := logger.Warn
	if cfg.AppEnv == "development" {
		logLevel = logger.Info
	}
	gormLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logLevel,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:      gormLogger,
		PrepareStmt: true,
	})
	if err != nil {
		log.Fatalf("[Database] Failed to connect to database (%s): %v", cfg.DBDriver, err)
	}

	// -------------------------------------------------------------
	// Connection pool (disesuaikan untuk VPS 2 core / 2 GB RAM)
	// -------------------------------------------------------------
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("[Database] Failed to get generic database object: %v", err)
	}

	if cfg.DBDriver != "sqlite" {
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)

		// Pantau antrean pool. Kalau waitCount terus naik, naikkan MaxOpenConns.
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			var lastWait int64
			for range ticker.C {
				s := sqlDB.Stats()
				// Cetak hanya jika ada antrean baru atau pool hampir penuh,
				// supaya log tidak membengkak lagi.
				if s.WaitCount > lastWait || s.InUse >= 15 {
					log.Printf("[DBPool] open=%d inUse=%d idle=%d waitCount=%d waitDuration=%s",
						s.OpenConnections, s.InUse, s.Idle, s.WaitCount, s.WaitDuration)
				}
				lastWait = s.WaitCount
			}
		}()
	} else {
		// SQLite: satu koneksi untuk menghindari "database is locked"
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
		&domain.ProviderProduct{},
		&domain.Transaction{},
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
		&domain.SystemSetting{},
	)
	if err != nil {
		log.Fatalf("[Database] Migration error: %v", err)
	}

	DB = db
	return db
}