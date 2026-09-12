// Package sharedconfig defines GlobalConfig, the single struct that carries
// every shared dependency (database, cache, blockchain client, notification
// providers, secrets) into each component's Init function. It is a manual
// service locator rather than a DI framework, matching the upstream
// project's pattern - simple to read, easy to trace, no reflection magic.
package sharedconfig

import (
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/cache"
	"wallet-backend/internal/fiat"
	"wallet-backend/internal/kyc"
	"wallet-backend/internal/network"
	"wallet-backend/internal/notify"
	"wallet-backend/internal/rates"
	"wallet-backend/internal/storage"
)

// GlobalConfig is passed by pointer to every component's Init function.
type GlobalConfig struct {
	DB         *gorm.DB
	Cache      cache.Cache
	Blockchain *network.Client
	ChainID    int64 // 8453 = Base Mainnet, 84532 = Base Sepolia - see PLAN.md §8

	Mailer  notify.Mailer
	SMS     notify.SMSProvider
	Push    notify.PushProvider
	Storage storage.Blob
	KYC     kyc.Provider
	Fiat    fiat.Processor // nil unless a project wires one in; see internal/fiat doc.
	Rates   rates.Provider
	Alerts  alerting.Notifier

	JWTSecret    string
	JWTExpiry    time.Duration
	SIWEDomain   string // the "domain" every SIWE sign-in message must declare
	Organisation string

	// GroupKeySalt seeds shared-access group key derivation (see
	// internal/components/sharedaccess) - change this and every existing
	// group's controlling address changes with it, so treat it like a
	// secret and never rotate it casually once groups exist in production.
	GroupKeySalt string

	// RecoveryAuthoritySalt seeds the account-recovery attestation key (see
	// internal/components/users/services/recovery.go) - same rotation
	// caution as GroupKeySalt.
	RecoveryAuthoritySalt string
	RecoveryOTPTTL        time.Duration
}

// Env holds every raw environment-derived setting. Load it once in main and
// use it to construct GlobalConfig's dependencies.
type Env struct {
	Port string

	DBType             string
	DBConnectionString string
	DBAutoMigrate      bool

	BaseRPCURL  string
	BaseChainID int64

	CacheEnabled  bool
	RedisHost     string
	RedisPort     string
	RedisPassword string

	SMTPHost         string
	SMTPPort         string
	SMTPUsername     string
	SMTPPassword     string
	MailFrom         string
	UseConsoleMailer bool

	StorageDir string
	StorageURL string

	DiscordWebhookURL string

	JWTSecret        string
	JWTExpiryMinutes int
	SIWEDomain       string

	GroupKeySalt string

	RecoveryAuthoritySalt string
	RecoveryOTPTTLMinutes int

	Organisation string
}

// LoadEnv reads configuration from the process environment, applying
// sensible development defaults so the service boots with a nearly-empty
// .env file. The one default worth flagging: BASE_CHAIN_ID defaults to
// 84532 (Base Sepolia, the testnet) rather than mainnet, so a forgotten
// env var can never accidentally point a fresh checkout at real funds.
func LoadEnv() Env {
	return Env{
		Port: getEnv("PORT", "8080"),

		DBType:             getEnv("DB_TYPE", "sqlite"),
		DBConnectionString: getEnv("DB_CONNECTION_STRING", "wallet-backend.sqlite"),
		DBAutoMigrate:      getEnvBool("DB_AUTOMIGRATE", true),

		BaseRPCURL:  getEnv("BASE_RPC_URL", "https://sepolia.base.org"),
		BaseChainID: getEnvInt64("BASE_CHAIN_ID", 84532),

		CacheEnabled:  getEnvBool("ENABLE_CACHING", false),
		RedisHost:     getEnv("REDIS_HOST", "localhost"),
		RedisPort:     getEnv("REDIS_PORT", "6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),

		SMTPHost:         getEnv("SMTP_HOST", ""),
		SMTPPort:         getEnv("SMTP_PORT", "587"),
		SMTPUsername:     getEnv("SMTP_USERNAME", ""),
		SMTPPassword:     getEnv("SMTP_PASSWORD", ""),
		MailFrom:         getEnv("MAIL_FROM", "no-reply@example.com"),
		UseConsoleMailer: getEnvBool("USE_CONSOLE_MAILER", true),

		StorageDir: getEnv("STORAGE_DIR", "./data/uploads"),
		StorageURL: getEnv("STORAGE_URL", "/files"),

		DiscordWebhookURL: getEnv("DISCORD_WEBHOOK_URL", ""),

		JWTSecret:        getEnv("JWT_SECRET", "dev-only-change-me"),
		JWTExpiryMinutes: getEnvInt("JWT_EXPIRY_MINUTES", 60),
		SIWEDomain:       getEnv("SIWE_DOMAIN", "localhost"),

		GroupKeySalt: getEnv("GROUP_KEY_SALT", "dev-only-change-me"),

		RecoveryAuthoritySalt: getEnv("RECOVERY_AUTHORITY_SALT", "dev-only-change-me"),
		RecoveryOTPTTLMinutes: getEnvInt("RECOVERY_OTP_TTL_MINUTES", 15),

		Organisation: getEnv("ORGANISATION", "wallet-backend"),
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
