// Package db opens the application database and runs migrations. It knows
// nothing about individual component models - main.go collects every
// component's model structs into one slice and passes it to MigrateDB, so
// this package never needs to import (or be imported by) any component.
package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// OpenDB opens a GORM connection for the given driver ("postgres" or
// "sqlite") and connection string.
func OpenDB(dbType, connectionString string) (*gorm.DB, error) {
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)}

	switch dbType {
	case "postgres":
		return gorm.Open(postgres.Open(connectionString), config)
	case "sqlite":
		// glebarez/sqlite is a pure-Go SQLite driver (no CGO), unlike the
		// upstream project's CGO sqlite-cipher fork - this keeps `go build`
		// working with no C toolchain, at the cost of column-level
		// encryption support. Use Postgres for anything beyond local dev.
		return gorm.Open(sqlite.Open(connectionString), config)
	default:
		return nil, fmt.Errorf("unsupported DB_TYPE %q (want \"postgres\" or \"sqlite\")", dbType)
	}
}

// MigrateDB runs AutoMigrate for every model passed in. Call it once at boot
// with the full list of component models (see main.go).
func MigrateDB(gormDB *gorm.DB, models ...interface{}) error {
	if err := gormDB.AutoMigrate(models...); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

// SetPoolLimits configures the underlying *sql.DB's connection pool -
// unset previously, meaning an unbounded (driver-default) pool with no
// exhaustion signal to alert on at all. Call once at boot, right after
// OpenDB (see PLAN.md §4.13 - "DB pool warnings").
func SetPoolLimits(gormDB *gorm.DB, maxOpenConns, maxIdleConns int, connMaxLifetime time.Duration) error {
	sqlDB, err := gormDB.DB()
	if err != nil {
		return fmt.Errorf("get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	return nil
}

// PoolStats returns the underlying *sql.DB's current connection-pool
// stats, for a periodic caller to check for exhaustion (see main.go's
// pool-monitor worker).
func PoolStats(gormDB *gorm.DB) (sql.DBStats, error) {
	sqlDB, err := gormDB.DB()
	if err != nil {
		return sql.DBStats{}, fmt.Errorf("get underlying sql.DB: %w", err)
	}
	return sqlDB.Stats(), nil
}
