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
	DB                *gorm.DB
	Cache             cache.Cache
	Blockchain        *network.Client
	NetworkPassphrase string

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
	AuthWindow   time.Duration
	Organisation string
}

// Env holds every raw environment-derived setting. Load it once in main and
// use it to construct GlobalConfig's dependencies.
type Env struct {
	Port string

	DBType             string
	DBConnectionString string
	DBAutoMigrate      bool

	HorizonURL  string
	NetworkMode string // "testnet" or "public"

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

	JWTSecret         string
	JWTExpiryMinutes  int
	AuthWindowSeconds int

	Organisation string
}

// LoadEnv reads configuration from the process environment, applying
// sensible development defaults so the service boots with a nearly-empty
// .env file.
func LoadEnv() Env {
	return Env{
		Port: getEnv("PORT", "8080"),

		DBType:             getEnv("DB_TYPE", "sqlite"),
		DBConnectionString: getEnv("DB_CONNECTION_STRING", "wallet-backend.sqlite"),
		DBAutoMigrate:      getEnvBool("DB_AUTOMIGRATE", true),

		HorizonURL:  getEnv("HORIZON_URL", ""),
		NetworkMode: getEnv("STELLAR_NETWORK", "testnet"),

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

		JWTSecret:         getEnv("JWT_SECRET", "dev-only-change-me"),
		JWTExpiryMinutes:  getEnvInt("JWT_EXPIRY_MINUTES", 60),
		AuthWindowSeconds: getEnvInt("AUTH_WINDOW_SECONDS", 60),

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
