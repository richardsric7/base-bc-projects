// Package db opens the database this engine shares with wallet-backend
// (one Postgres instance, per PLAN.md §2's decision to fold the original's
// second dedicated CockroachDB into a single shared database) and runs
// this engine's own migrations. It never migrates wallet-backend's own
// tables (users, user_wallets, curated_tokens) - those are read-only
// mirrors here, owned and migrated by wallet-backend itself.
package db

import (
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenDB opens a GORM connection for the given driver ("postgres" or
// "sqlite" - sqlite for local dev/tests only, matching wallet-backend's
// own convention).
func OpenDB(dbType, connectionString string) (*gorm.DB, error) {
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)}

	switch dbType {
	case "postgres":
		return gorm.Open(postgres.Open(connectionString), config)
	case "sqlite":
		return gorm.Open(sqlite.Open(connectionString), config)
	default:
		return nil, fmt.Errorf("unsupported DB_TYPE %q (want \"postgres\" or \"sqlite\")", dbType)
	}
}

// MigrateDB runs AutoMigrate for every model this engine owns. Call it
// once at boot with only this engine's own models (see main.go) - never
// wallet-backend's.
func MigrateDB(gormDB *gorm.DB, models ...interface{}) error {
	if err := gormDB.AutoMigrate(models...); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}
