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

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/cache"
	"wallet-backend/internal/fiat"
	"wallet-backend/internal/geoip"
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
	// AddressWatcher polls for Transfer/Approval logs touching addresses a
	// component has cached a response for, so it can invalidate that cache
	// entry - the Base substitute for Horizon operation streaming (PLAN.md
	// §2, §5). Always non-nil; a component only needs to use it if it
	// caches something keyed by an on-chain address.
	AddressWatcher *network.AddressWatcher

	Mailer  notify.Mailer
	SMS     notify.SMSProvider
	Push    notify.PushProvider
	Storage storage.Blob
	KYC     kyc.Provider
	Fiat    fiat.Processor // nil unless a project wires one in; see internal/fiat doc.
	Rates   rates.Provider
	Alerts  alerting.Notifier
	// GeoIP resolves a registering caller's IP to a country code for the
	// registration risk fields (see internal/geoip, PLAN.md §4.13).
	// Always non-nil - geoip.NewNoopProvider() when no vendor is
	// configured, so users.Service.Register behaves identically either way.
	GeoIP geoip.Provider

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

	// Sumsub/Doja credentials for internal/components/kyc. The upstream
	// project stored these in a database KYCConfig table; this port keeps
	// them as env-sourced secrets instead, consistent with every other
	// credential in this struct, and to avoid an API secret sitting in
	// plaintext in the same database as user data.
	SumsubBaseURL   string
	SumsubToken     string
	SumsubSecretKey string
	DojaSecretKey   string

	// FaucetKeySalt seeds the activation-faucet key derivation (see
	// internal/components/fiat) - same rotation caution as GroupKeySalt.
	// ActivationRewardTokenSymbol is the CuratedToken symbol activation
	// dispenses alongside starter gas; empty disables the reward-token half
	// (only gas is sent).
	FaucetKeySalt               string
	ActivationRewardTokenSymbol string

	// FlutterwaveSecretHash is the shared secret Flutterwave echoes back in
	// its webhook's "verif-hash" header (its dashboard's "Secret Hash"
	// setting, not a payment key) - see internal/fiat/flutterwave.
	FlutterwaveSecretHash string

	// Stablerail credentials for internal/components/stablerail - same
	// env-sourced-secret rationale as Sumsub/Doja/Flutterwave above.
	StablerailAPIKey  string
	StablerailBaseURL string
	StablerailEnabled bool

	// OneLiquidity credentials and CryptoTreasuryKeySalt for
	// internal/components/crypto - same env-sourced-secret/derived-key
	// rationale as every other vendor integration in this struct.
	OneLiquidityBaseURL               string
	OneLiquidityToken                 string
	CryptoWalletDomain                string
	CryptoTreasuryKeySalt             string
	CryptoWithdrawalServiceFeePercent float64

	// MarketEscrowKeySalt seeds the market-making escrow key derivation
	// (see internal/components/market) - same rotation caution as
	// GroupKeySalt: a maker's approve() targets the address this
	// currently derives to, so rotating it orphans any standing
	// approvals.
	MarketEscrowKeySalt string

	// TokenizationIssuerKeySalt/TokenizationDistributionKeySalt seed the
	// per-asset issuer (mint authority) and distribution (treasury/
	// proceeds) key derivations (see internal/components/tokenization) -
	// same rotation caution as MarketEscrowKeySalt: rotating either
	// orphans every already-minted asset's on-chain contract ownership.
	// TokenizationTokenLimit caps NumberOfTokenToBeIssued on a new
	// application; zero means no cap.
	TokenizationIssuerKeySalt       string
	TokenizationDistributionKeySalt string
	TokenizationTokenLimit          decimal.Decimal

	// PatronFeeWalletSalt seeds the patron-subscription fee wallet key
	// derivation (see internal/components/patron) - same rotation
	// caution as every other derived fee-collection address in this port.
	PatronFeeWalletSalt string
	PatronVATPercent    float64

	// ServiceLinkApprovalTTL is how long a partner's login/authorize/event
	// consent request (see internal/components/servicelinks) stays pending
	// before it expires unactioned.
	ServiceLinkApprovalTTL time.Duration

	// ShortlinkBaseURL is prefixed to a short code to build the public
	// short URL a QR code encodes (see internal/components/shortlink,
	// PLAN.md §4.13) - e.g. "https://trov.to" for "https://trov.to/s/AB12CD34".
	ShortlinkBaseURL string
}

// Env holds every raw environment-derived setting. Load it once in main and
// use it to construct GlobalConfig's dependencies.
type Env struct {
	Port string

	DBType             string
	DBConnectionString string
	DBAutoMigrate      bool
	// DBMaxOpenConns/DBMaxIdleConns/DBConnMaxLifetimeMinutes configure the
	// connection pool (see internal/db.SetPoolLimits) - unset previously,
	// meaning an unbounded pool with no exhaustion signal to alert on at
	// all (PLAN.md §4.13's "DB pool warnings").
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	DBConnMaxLifetimeMinutes int

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

	SumsubBaseURL   string
	SumsubToken     string
	SumsubSecretKey string
	DojaSecretKey   string

	FaucetKeySalt                string
	ActivationRewardTokenSymbol  string
	FaucetLowBalanceThresholdETH float64

	FlutterwaveSecretKey  string
	FlutterwaveSecretHash string

	StablerailAPIKey  string
	StablerailBaseURL string
	StablerailEnabled bool

	OneLiquidityBaseURL               string
	OneLiquidityToken                 string
	CryptoWalletDomain                string
	CryptoTreasuryKeySalt             string
	CryptoWithdrawalServiceFeePercent float64

	MarketEscrowKeySalt string

	TokenizationIssuerKeySalt       string
	TokenizationDistributionKeySalt string
	TokenizationTokenLimit          string

	PatronFeeWalletSalt string
	PatronVATPercent    float64

	ServiceLinkApprovalTTLMinutes int

	// GeoIPBaseURL points at an ipapi.co-shaped free-text country lookup
	// (GET {baseURL}/{ip}/country/); empty disables geo-IP lookup entirely
	// (geoip.NewNoopProvider is used instead) - see PLAN.md §4.13.
	GeoIPBaseURL string

	ShortlinkBaseURL string

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

		DBMaxOpenConns:           getEnvInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:           getEnvInt("DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetimeMinutes: getEnvInt("DB_CONN_MAX_LIFETIME_MINUTES", 30),

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

		SumsubBaseURL:   getEnv("SUMSUB_BASE_URL", "https://api.sumsub.com"),
		SumsubToken:     getEnv("SUMSUB_TOKEN", ""),
		SumsubSecretKey: getEnv("SUMSUB_SECRET_KEY", ""),
		DojaSecretKey:   getEnv("DOJA_SECRET_KEY", ""),

		FaucetKeySalt:                getEnv("FAUCET_KEY_SALT", "dev-only-change-me"),
		ActivationRewardTokenSymbol:  getEnv("ACTIVATION_REWARD_TOKEN_SYMBOL", ""),
		FaucetLowBalanceThresholdETH: getEnvFloat64("FAUCET_LOW_BALANCE_THRESHOLD_ETH", 0),

		FlutterwaveSecretKey:  getEnv("FLUTTERWAVE_SECRET_KEY", ""),
		FlutterwaveSecretHash: getEnv("FLUTTERWAVE_SECRET_HASH", ""),

		StablerailAPIKey:  getEnv("STABLERAIL_API_KEY", ""),
		StablerailBaseURL: getEnv("STABLERAIL_BASE_URL", "https://beta.stablesrail.io/v1"),
		StablerailEnabled: getEnvBool("STABLERAIL_ENABLED", false),

		OneLiquidityBaseURL:               getEnv("ONELIQUIDITY_BASE_URL", "https://sandbox-api.oneliquidity.technology"),
		OneLiquidityToken:                 getEnv("ONELIQUIDITY_TOKEN", ""),
		CryptoWalletDomain:                getEnv("CRYPTO_WALLET_DOMAIN", "wallet-backend"),
		CryptoTreasuryKeySalt:             getEnv("CRYPTO_TREASURY_KEY_SALT", "dev-only-change-me"),
		CryptoWithdrawalServiceFeePercent: getEnvFloat64("CRYPTO_WITHDRAWAL_SERVICE_FEE_PERCENT", 1.0),

		MarketEscrowKeySalt: getEnv("MARKET_ESCROW_KEY_SALT", "dev-only-change-me"),

		TokenizationIssuerKeySalt:       getEnv("TOKENIZATION_ISSUER_KEY_SALT", "dev-only-change-me"),
		TokenizationDistributionKeySalt: getEnv("TOKENIZATION_DISTRIBUTION_KEY_SALT", "dev-only-change-me"),
		TokenizationTokenLimit:          getEnv("TOKENIZATION_TOKEN_LIMIT", "0"),

		PatronFeeWalletSalt: getEnv("PATRON_FEE_WALLET_SALT", "dev-only-change-me"),
		PatronVATPercent:    getEnvFloat64("PATRON_VAT_PERCENT", 0),

		ServiceLinkApprovalTTLMinutes: getEnvInt("SERVICELINK_APPROVAL_TTL_MINUTES", 10),

		GeoIPBaseURL: getEnv("GEOIP_BASE_URL", ""),

		ShortlinkBaseURL: getEnv("SHORTLINK_BASE_URL", "http://localhost:8080"),

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

func getEnvFloat64(key string, fallback float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}
