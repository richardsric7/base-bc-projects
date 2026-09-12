// Package db opens the application database and runs migrations. It knows
// nothing about individual component models - main.go collects every
// component's model structs into one slice and passes it to MigrateDB, so
// this package never needs to import (or be imported by) any component.
package db

import (
	"fmt"

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
