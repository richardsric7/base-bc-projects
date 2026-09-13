// Package sharedconfig defines GlobalConfig, the single struct threading
// every shared dependency through this service's boot sequence - the same
// manual-service-locator pattern wallet-backend uses (see its own
// sharedconfig package), kept independent here since this is a separate
// deployable with its own go.mod, not a component inside wallet-backend.
package sharedconfig

import (
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"

	"wallet-payment-history-engine/internal/alerting"
	"wallet-payment-history-engine/internal/network"
	"wallet-payment-history-engine/internal/notify"
)

// GlobalConfig carries every shared dependency into the engine's workers.
type GlobalConfig struct {
	DB         *gorm.DB
	Blockchain *network.Client
	ChainID    int64

	Push   notify.Provider
	Alerts alerting.Notifier

	// PollInterval is how often the live indexer checks for new blocks -
	// see PLAN.md §3.2 ("every ~4s, matching AddressWatcher's cadence").
	PollInterval time.Duration
}

// Env holds every raw environment-derived setting, loaded once in main.
type Env struct {
	DBType             string
	DBConnectionString string
	DBAutoMigrate      bool

	BaseRPCURL  string
	BaseChainID int64

	PollIntervalSeconds int

	DiscordWebhookURL string

	// BackfillFloorBlock is the earliest block the backfill watcher will
	// scan for a newly tracked wallet - see PLAN.md §3.2's "no pre-existence
	// to backfill before this point" note. 0 means genesis.
	BackfillFloorBlock uint64
}

// LoadEnv reads configuration from the process environment.
func LoadEnv() Env {
	return Env{
		DBType:             getEnv("DB_TYPE", "sqlite"),
		DBConnectionString: getEnv("DB_CONNECTION_STRING", "wallet-payment-history-engine.sqlite"),
		DBAutoMigrate:      getEnvBool("DB_AUTOMIGRATE", true),

		BaseRPCURL:  getEnv("BASE_RPC_URL", "https://sepolia.base.org"),
		BaseChainID: getEnvInt64("BASE_CHAIN_ID", 84532),

		PollIntervalSeconds: getEnvInt("POLL_INTERVAL_SECONDS", 4),

		DiscordWebhookURL: getEnv("DISCORD_WEBHOOK_URL", ""),

		BackfillFloorBlock: getEnvUint64("BACKFILL_FLOOR_BLOCK", 0),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getEnvInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func getEnvInt64(key string, fallback int64) int64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return i
}

func getEnvUint64(key string, fallback uint64) uint64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	i, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return fallback
	}
	return i
}
