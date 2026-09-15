// Command wallet-backend boots the API server: load config, open the
// database, run migrations, construct every shared dependency, and hand
// them to each component's Init function. See internal/sharedconfig for the
// GlobalConfig struct threaded through the whole boot sequence, and
// PLAN.md for the architecture this mirrors.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/cache"
	announcementsModels "wallet-backend/internal/components/announcements/models"
	assetsModels "wallet-backend/internal/components/assets/models"
	cryptoModels "wallet-backend/internal/components/crypto/models"
	fiatModels "wallet-backend/internal/components/fiat/models"
	kycModels "wallet-backend/internal/components/kyc/models"
	marketModels "wallet-backend/internal/components/market/models"
	patronModels "wallet-backend/internal/components/patron/models"
	paymentsModels "wallet-backend/internal/components/payments/models"
	referenceModels "wallet-backend/internal/components/reference/models"
	servicelinksModels "wallet-backend/internal/components/servicelinks/models"
	shortlinkModels "wallet-backend/internal/components/shortlink/models"
	tokenizationModels "wallet-backend/internal/components/tokenization/models"
	usersModels "wallet-backend/internal/components/users/models"

	announcementsControllers "wallet-backend/internal/components/announcements/controllers"
	assetsControllers "wallet-backend/internal/components/assets/controllers"
	callbacksControllers "wallet-backend/internal/components/callbacks/controllers"
	cryptoControllers "wallet-backend/internal/components/crypto/controllers"
	docsControllers "wallet-backend/internal/components/docs/controllers"
	fiatControllers "wallet-backend/internal/components/fiat/controllers"
	kycControllers "wallet-backend/internal/components/kyc/controllers"
	marketControllers "wallet-backend/internal/components/market/controllers"
	patronControllers "wallet-backend/internal/components/patron/controllers"
	paymentsControllers "wallet-backend/internal/components/payments/controllers"
	ratesControllers "wallet-backend/internal/components/rates/controllers"
	referenceControllers "wallet-backend/internal/components/reference/controllers"
	rootControllers "wallet-backend/internal/components/root/controllers"
	servicelinksControllers "wallet-backend/internal/components/servicelinks/controllers"
	sharedaccessControllers "wallet-backend/internal/components/sharedaccess/controllers"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	shortlinkControllers "wallet-backend/internal/components/shortlink/controllers"
	stablerailControllers "wallet-backend/internal/components/stablerail/controllers"
	stablerailModels "wallet-backend/internal/components/stablerail/models"
	swapsControllers "wallet-backend/internal/components/swaps/controllers"
	tokenizationControllers "wallet-backend/internal/components/tokenization/controllers"
	usersControllers "wallet-backend/internal/components/users/controllers"

	"wallet-backend/internal/db"
	"wallet-backend/internal/fiat"
	"wallet-backend/internal/fiat/flutterwave"
	"wallet-backend/internal/geoip"
	"wallet-backend/internal/kyc"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/network"
	"wallet-backend/internal/notify"
	"wallet-backend/internal/rates"
	"wallet-backend/internal/relayer"
	"wallet-backend/internal/sharedconfig"
	"wallet-backend/internal/storage"
)

// allModels is the full migration list. Add a component's Models var here
// when you add a component.
func allModels() []interface{} {
	var models []interface{}
	models = append(models, usersModels.Models...)
	models = append(models, assetsModels.Models...)
	models = append(models, paymentsModels.Models...)
	models = append(models, announcementsModels.Models...)
	models = append(models, sharedaccessModels.Models...)
	models = append(models, kycModels.Models...)
	models = append(models, fiatModels.Models...)
	models = append(models, stablerailModels.Models...)
	models = append(models, cryptoModels.Models...)
	models = append(models, marketModels.Models...)
	models = append(models, tokenizationModels.Models...)
	models = append(models, patronModels.Models...)
	models = append(models, servicelinksModels.Models...)
	models = append(models, referenceModels.Models...)
	models = append(models, shortlinkModels.Models...)
	return models
}

func main() {
	// A missing .env is fine (e.g. in a container with real env vars set) -
	// only log, never fail, on load error.
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on process environment")
	}

	env := sharedconfig.LoadEnv()

	gormDB, err := db.OpenDB(env.DBType, env.DBConnectionString)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	if err := db.SetPoolLimits(gormDB, env.DBMaxOpenConns, env.DBMaxIdleConns, time.Duration(env.DBConnMaxLifetimeMinutes)*time.Minute); err != nil {
		log.Fatalf("failed to configure database connection pool: %v", err)
	}

	if env.DBAutoMigrate {
		if err := db.MigrateDB(gormDB, allModels()...); err != nil {
			log.Fatalf("failed to migrate database: %v", err)
		}
	}

	seedSecurityQuestions(gormDB)
	seedReservedNames(gormDB)
	seedSumsubLevels(gormDB)
	seedActivationConfig(gormDB)
	seedPatronCatalog(gormDB)

	appCache := cache.NewNoopCache()
	if env.CacheEnabled {
		redisCache, err := cache.NewRedisCache(env.RedisHost, env.RedisPort, env.RedisPassword)
		if err != nil {
			log.Fatalf("ENABLE_CACHING is set but Redis is unreachable: %v", err)
		}
		appCache = redisCache
		log.Println("cache: connected to Redis")
	} else {
		log.Println("cache: disabled (set ENABLE_CACHING=true to enable Redis)")
	}

	blockchain, err := network.NewClient(context.Background(), env.BaseRPCURL, env.BaseChainID)
	if err != nil {
		log.Fatalf("failed to connect to Base RPC %q: %v", env.BaseRPCURL, err)
	}
	log.Printf("blockchain: Base chain %d (rpc=%s)", env.BaseChainID, env.BaseRPCURL)

	addressWatcher := network.NewAddressWatcher(blockchain)

	relayerPool, err := relayer.NewPool(env.RelayerKeySalt, env.RelayerPoolSize)
	if err != nil {
		log.Fatalf("failed to derive the shared-access relayer pool: %v", err)
	}
	log.Printf("relayer: pool of %d addresses derived (fund them with ETH before enabling shared-access execution)", len(relayerPool.Addresses()))

	var mailer notify.Mailer = notify.NewConsoleMailer()
	if !env.UseConsoleMailer && env.SMTPHost != "" {
		mailer = notify.NewSMTPMailer(env.SMTPHost, env.SMTPPort, env.SMTPUsername, env.SMTPPassword, env.MailFrom)
		log.Printf("mail: sending via SMTP host %s", env.SMTPHost)
	} else {
		log.Println("mail: using console mailer (set USE_CONSOLE_MAILER=false and SMTP_HOST to send real email)")
	}

	blobStorage, err := storage.NewLocalDiskBlob(env.StorageDir, env.StorageURL)
	if err != nil {
		log.Fatalf("failed to initialize local file storage: %v", err)
	}

	var alerts alerting.Notifier = alerting.NewNoopNotifier()
	if env.DiscordWebhookURL != "" {
		alerts = alerting.NewDiscordWebhookNotifier(env.DiscordWebhookURL)
	}

	var fiatProcessor fiat.Processor
	if env.FlutterwaveSecretKey != "" {
		fiatProcessor = flutterwave.New(env.FlutterwaveSecretKey, env.FlutterwaveSecretHash)
		log.Println("fiat: Flutterwave processor configured")
	} else {
		log.Println("fiat: no processor configured (set FLUTTERWAVE_SECRET_KEY to enable Flutterwave)")
	}

	var geoIPProvider geoip.Provider = geoip.NewNoopProvider()
	if env.GeoIPBaseURL != "" {
		geoIPProvider = geoip.NewIPAPIProvider(env.GeoIPBaseURL)
		log.Printf("geoip: registration risk lookup enabled via %s", env.GeoIPBaseURL)
	} else {
		log.Println("geoip: disabled (set GEOIP_BASE_URL to enable registration risk lookup)")
	}

	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{
		// Example fixtures - replace with a live provider or DB-backed
		// table for production use.
		"USD/ETH": decimal.NewFromFloat(0.00028),
		"ETH/USD": decimal.NewFromFloat(3500),
	})

	gc := &sharedconfig.GlobalConfig{
		DB:             gormDB,
		Cache:          appCache,
		Blockchain:     blockchain,
		ChainID:        env.BaseChainID,
		AddressWatcher: addressWatcher,

		Mailer:  mailer,
		SMS:     notify.NewConsoleSMSProvider(),
		Push:    notify.NewConsolePushProvider(),
		Storage: blobStorage,
		KYC:     kyc.NewManualKYCProvider(),
		Fiat:    fiatProcessor,
		Rates:   ratesProvider,
		Alerts:  alerts,
		GeoIP:   geoIPProvider,

		JWTSecret: env.JWTSecret,
		JWTExpiry: durationFromMinutes(env.JWTExpiryMinutes),

		SignatureAuthToleranceSeconds: env.SignatureAuthToleranceSeconds,
		Organisation:                  env.Organisation,

		RecoveryAuthoritySalt: env.RecoveryAuthoritySalt,
		RecoveryOTPTTL:        durationFromMinutes(env.RecoveryOTPTTLMinutes),

		SafeDeployerKeySalt: env.SafeDeployerKeySalt,
		RelayerPool:         relayerPool,

		SumsubBaseURL:   env.SumsubBaseURL,
		SumsubToken:     env.SumsubToken,
		SumsubSecretKey: env.SumsubSecretKey,
		DojaSecretKey:   env.DojaSecretKey,

		FaucetKeySalt:               env.FaucetKeySalt,
		ActivationRewardTokenSymbol: env.ActivationRewardTokenSymbol,
		FlutterwaveSecretHash:       env.FlutterwaveSecretHash,

		StablerailAPIKey:  env.StablerailAPIKey,
		StablerailBaseURL: env.StablerailBaseURL,
		StablerailEnabled: env.StablerailEnabled,

		OneLiquidityBaseURL:               env.OneLiquidityBaseURL,
		OneLiquidityToken:                 env.OneLiquidityToken,
		CryptoWalletDomain:                env.CryptoWalletDomain,
		CryptoTreasuryKeySalt:             env.CryptoTreasuryKeySalt,
		CryptoWithdrawalServiceFeePercent: env.CryptoWithdrawalServiceFeePercent,

		MarketEscrowKeySalt: env.MarketEscrowKeySalt,

		TokenizationIssuerKeySalt:       env.TokenizationIssuerKeySalt,
		TokenizationDistributionKeySalt: env.TokenizationDistributionKeySalt,
		TokenizationTokenLimit:          parseDecimalOrZero(env.TokenizationTokenLimit),

		PatronFeeWalletSalt: env.PatronFeeWalletSalt,
		PatronVATPercent:    env.PatronVATPercent,

		ServiceLinkApprovalTTL: durationFromMinutes(env.ServiceLinkApprovalTTLMinutes),
		PendingActionTTL:       durationFromMinutes(env.PendingActionTTLMinutes),

		RecoveryOperatorKeySalts: splitAndTrim(env.RecoveryOperatorKeySaltsRaw),
		RecoveryServiceThreshold: env.RecoveryServiceThreshold,
		WalletRecoveryFeeWei:     decimal.NewFromFloat(env.WalletRecoveryFeeETH).Mul(decimal.New(1, 18)).BigInt(),
	}

	router := gin.Default()
	router.Use(middleware.CORS())
	router.Static(env.StorageURL, env.StorageDir)

	rootControllers.Init(router, gc)
	docsControllers.Init(router)
	usersSvc := usersControllers.Init(router, gc)
	usersSvc.GeoIP = gc.GeoIP
	// PLAN.md §15's wallet-recovery Branch B platform infrastructure -
	// must run before the router starts serving traffic, the same
	// placement as sharedaccessSvc.ReconcileRelayers below (enrollment
	// calls need the recovery-service Safe/RecoveryGuard addresses to
	// already be resolved). A no-op if RECOVERY_OPERATOR_KEY_SALTS was
	// never configured - Branch B then simply stays unavailable. Logged
	// rather than fatal on failure, the same non-blocking posture
	// ReconcileRelayers below takes: this is one optional feature among
	// many components this server hosts, so a transient RPC failure here
	// must not take the whole server down.
	if err := usersSvc.EnsureRecoveryPlatformDeployed(context.Background()); err != nil {
		log.Printf("wallet recovery (Branch B) platform infrastructure failed to deploy, Branch B will be unavailable until this is resolved and the server restarts: %v", err)
	}
	assetsSvc := assetsControllers.Init(router, gc)
	paymentsSvc := paymentsControllers.Init(router, gc)
	paymentsSvc.Alerts = gc.Alerts
	swapsSvc := swapsControllers.Init(router, gc)
	swapsSvc.Alerts = gc.Alerts
	sharedaccessSvc := sharedaccessControllers.Init(router, gc)
	// PLAN.md §13.10 Phase 8's curated-asset balance summary - assigned
	// post-construction, the same cross-component wiring pattern as
	// paymentsSvc.Alerts/usersSvc.GeoIP above.
	sharedaccessSvc.Assets = assetsSvc
	// PLAN.md §13.9's flagged follow-up, closed: payments/swaps/assets'
	// approve delegate the actual Safe transaction to sharedaccess rather
	// than building an unsignable raw EIP-1559 transaction "from" a Safe
	// address.
	paymentsSvc.SharedAccess = sharedaccessSvc
	swapsSvc.SharedAccess = sharedaccessSvc
	assetsSvc.SharedAccess = sharedaccessSvc
	// Must run before the router starts serving traffic - see
	// relayer.Pool.ReserveAtStartup's own doc comment on why it isn't
	// safe to call once the pool is already handling concurrent Claims.
	sharedaccessSvc.ReconcileRelayers(context.Background())
	ratesControllers.Init(router, gc)
	announcementsControllers.Init(router, gc)
	callbacksControllers.Init(router, gc)
	kycSvc := kycControllers.Init(router, gc)
	fiatSvc := fiatControllers.Init(router, gc)
	fiatSvc.Alerts = gc.Alerts
	if env.FaucetLowBalanceThresholdETH > 0 {
		fiatSvc.FaucetLowBalanceThresholdWei = decimal.NewFromFloat(env.FaucetLowBalanceThresholdETH).Mul(decimal.New(1, 18)).BigInt()
	}
	stablerailSvc := stablerailControllers.Init(router, gc)
	cryptoSvc := cryptoControllers.Init(router, gc)
	marketControllers.Init(router, gc)
	tokenizationSvc := tokenizationControllers.Init(router, gc)
	patronSvc := patronControllers.Init(router, gc)
	servicelinksSvc := servicelinksControllers.Init(router, gc, usersSvc, paymentsSvc, assetsSvc, tokenizationSvc)
	referenceControllers.Init(router, gc)
	shortlinkSvc := shortlinkControllers.Init(router, gc)
	// PLAN.md §14.2 item 2's shortlink/QR minting - assigned
	// post-construction like paymentsSvc.Alerts/usersSvc.GeoIP/
	// sharedaccessSvc.Assets above, since shortlinkControllers.Init runs
	// after servicelinksControllers.Init in this boot order.
	servicelinksSvc.Shortlink = shortlinkSvc

	// Wire the KYC component's Doja BVN-completion hook to Stablerail
	// onboarding - see kyc/services.Service.OnBVNVerified's doc comment
	// for why this is a post-construction callback rather than a
	// constructor argument (avoids kyc importing stablerail directly).
	kycSvc.OnBVNVerified = stablerailSvc.InitiateOnboardingByUsername

	// Wire tokenization's fiat purchase flow onto the exact same generic
	// invoice pattern the fiat component's own activation flow uses - see
	// tokenization/services.CreateFiatInvoiceFunc's doc comment for why
	// this is a callback rather than tokenization importing fiat/services
	// directly.
	tokenizationSvc.CreateFiatInvoice = func(address, id, serviceProvider, paymentType string, amount float64, currency string, signedTransaction *string) error {
		_, err := fiatSvc.CreateInvoice(address, id, serviceProvider, paymentType, amount, currency, signedTransaction)
		return err
	}

	if env.StablerailEnabled {
		go func() {
			for {
				stablerailSvc.PollPendingOnboarding()
				time.Sleep(10 * time.Second)
				stablerailSvc.PollPendingOnramp()
				time.Sleep(10 * time.Second)
			}
		}()
		go func() {
			for {
				if err := stablerailSvc.SyncSupportedBanks(); err != nil {
					log.Printf("[stablerail] error syncing supported banks: %v", err)
				}
				time.Sleep(10 * time.Minute)
			}
		}()
	}

	if env.OneLiquidityToken != "" {
		go func() {
			for {
				cryptoSvc.PollNewDeposits()
				time.Sleep(60 * time.Second)
			}
		}()
	}

	// Proactive low-balance warning, distinct from ProcessActivation's own
	// alert on an actual dispense failure - opt-in via
	// FAUCET_LOW_BALANCE_THRESHOLD_ETH (PLAN.md §4.13).
	if fiatSvc.FaucetLowBalanceThresholdWei != nil {
		go func() {
			for {
				if err := fiatSvc.CheckFaucetBalance(context.Background()); err != nil {
					log.Printf("[fiat] faucet balance check failed: %v", err)
				}
				time.Sleep(30 * time.Minute)
			}
		}()
	}

	// A single clean poll interval, replacing upstream's own accidental
	// 15-minute-sleep-inside-a-5-second-loop stacking (PLAN.md §4.9).
	go func() {
		for {
			tokenizationSvc.ActivatePrimarySales()
			tokenizationSvc.ActivateSecondarySales(context.Background())
			time.Sleep(30 * time.Second)
		}
	}()

	// A proper recurring worker - upstream's equivalent runs once at boot
	// only, so a subscription that becomes effective while the process
	// keeps running past that point is never promoted until the next
	// restart (PLAN.md §4.10, fixed rather than reproduced).
	go func() {
		for {
			patronSvc.PromotePendingMemberships()
			time.Sleep(30 * time.Second)
		}
	}()

	// Stale shared-access action expiry - the same 30-minute cadence the
	// original uses for its own fiat-invoice expiry, applied to a flow
	// that never had a timeout of its own (PLAN.md §13.10 Phase 6/§13.12).
	go func() {
		for {
			if affected, err := sharedaccessSvc.ExpireStalePendingActions(gc.PendingActionTTL); err != nil {
				log.Printf("[sharedaccess] failed to expire stale pending actions: %v", err)
			} else if affected > 0 {
				log.Printf("[sharedaccess] expired %d stale pending action(s)", affected)
			}
			time.Sleep(30 * time.Minute)
		}
	}()

	// DB pool exhaustion warning: alert once InUse hits the configured
	// ceiling, or once callers have had to wait for a connection at all
	// (WaitCount increasing since the last check) - either means the pool
	// is undersized for current load (PLAN.md §4.13).
	go func() {
		var lastWaitCount int64
		for {
			stats, err := db.PoolStats(gormDB)
			if err != nil {
				log.Printf("[db] failed to read connection pool stats: %v", err)
			} else {
				if stats.InUse >= env.DBMaxOpenConns {
					_ = alerts.Notify(fmt.Sprintf("database connection pool saturated: %d/%d connections in use", stats.InUse, env.DBMaxOpenConns))
				}
				if stats.WaitCount > lastWaitCount {
					_ = alerts.Notify(fmt.Sprintf("database connection pool exhausted %d time(s) since last check - callers are waiting for a connection", stats.WaitCount-lastWaitCount))
				}
				lastWaitCount = stats.WaitCount
			}
			time.Sleep(1 * time.Minute)
		}
	}()

	// Roughly every two Base blocks (~4s at Base's ~2s block time) per
	// PLAN.md §2 - the polling-based replacement for Horizon operation
	// streaming's role in cache invalidation.
	go func() {
		for {
			if err := addressWatcher.Poll(context.Background()); err != nil {
				log.Printf("[network] address watcher poll failed: %v", err)
			}
			time.Sleep(4 * time.Second)
		}
	}()

	log.Printf("%s listening on :%s", env.Organisation, env.Port)
	if err := router.Run(":" + env.Port); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

func durationFromMinutes(minutes int) time.Duration {
	return time.Duration(minutes) * time.Minute
}

// parseDecimalOrZero parses a decimal-string env var, defaulting to zero
// (meaning "no limit" for TokenizationTokenLimit) on anything unparseable.
func parseDecimalOrZero(value string) decimal.Decimal {
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero
	}
	return parsed
}

// splitAndTrim splits a comma-separated env var into its trimmed,
// non-empty parts - used for RecoveryOperatorKeySaltsRaw (PLAN.md §15.9
// Phase 1), the one config value in this codebase that's a genuine list
// of distinct secrets rather than a single derived-key salt.
func splitAndTrim(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// seedSecurityQuestions inserts a small default catalog on first boot so the
// account-recovery flow has something to work with out of the box.
func seedSecurityQuestions(gormDB *gorm.DB) {
	var count int64
	gormDB.Model(&usersModels.SecurityQuestion{}).Count(&count)
	if count > 0 {
		return
	}
	defaults := []usersModels.SecurityQuestion{
		{Question: "What was the name of your first pet?"},
		{Question: "What city were you born in?"},
		{Question: "What was your childhood nickname?"},
	}
	if err := gormDB.Create(&defaults).Error; err != nil {
		log.Printf("warning: failed to seed default security questions: %v", err)
	}
}

// seedReservedNames inserts a small default blocklist on first boot so
// obviously staff/brand/impersonation-prone usernames are never available,
// even before an operator has customized the list.
func seedReservedNames(gormDB *gorm.DB) {
	var count int64
	gormDB.Model(&usersModels.ReservedName{}).Count(&count)
	if count > 0 {
		return
	}
	defaults := []usersModels.ReservedName{
		{Name: "admin"},
		{Name: "administrator"},
		{Name: "root"},
		{Name: "support"},
		{Name: "wallet-backend"},
		{Name: "trovo"},
		{Name: "system"},
		{Name: "moderator"},
		{Name: "help"},
		{Name: "security"},
	}
	if err := gormDB.Create(&defaults).Error; err != nil {
		log.Printf("warning: failed to seed default reserved names: %v", err)
	}
}

// seedSumsubLevels inserts a small default catalog of Sumsub level names on
// first boot. These match Sumsub's own placeholder level-naming convention
// so InitiateSumsubLevel has something valid to request out of the box;
// an operator's actual Sumsub dashboard levels can be configured by editing
// this table directly (see PLAN.md §4.4 for why widget/level catalogs
// aren't hardcoded further than this).
func seedSumsubLevels(gormDB *gorm.DB) {
	var count int64
	gormDB.Model(&kycModels.SumsubLevel{}).Count(&count)
	if count > 0 {
		return
	}
	defaults := []kycModels.SumsubLevel{
		{Name: "id-and-liveness-level-1", Description: "Level 1: ID document and liveness check"},
		{Name: "id-and-liveness-level-2", Description: "Level 2: additional proof of address"},
		{Name: "id-and-liveness-level-3", Description: "Level 3: enhanced due diligence"},
	}
	if err := gormDB.Create(&defaults).Error; err != nil {
		log.Printf("warning: failed to seed default Sumsub levels: %v", err)
	}
}

// seedActivationConfig inserts the single default activation-price row on
// first boot, so /v1/fiat/activate has something to quote out of the box.
// An operator tunes the live price by editing this one row directly (see
// fiatModels.ActivationConfig's doc comment for why there's no admin UI or
// per-country matrix here yet).
func seedActivationConfig(gormDB *gorm.DB) {
	var count int64
	gormDB.Model(&fiatModels.ActivationConfig{}).Count(&count)
	if count > 0 {
		return
	}
	if err := gormDB.Create(&fiatModels.ActivationConfig{}).Error; err != nil {
		log.Printf("warning: failed to seed default activation config: %v", err)
	}
}

// seedPatronCatalog inserts the fixed GOLD/PLATINUM/DIAMOND package and
// MONTHLY/ANNUAL/LIFETIME tier catalog upstream hardcodes into its own
// upgrade/downgrade comparisons (see patron/services.validateUpgrade) -
// these IDs are load-bearing, not just labels, so they're seeded once
// rather than left for an operator to configure differently.
func seedPatronCatalog(gormDB *gorm.DB) {
	var packageCount int64
	gormDB.Model(&patronModels.PatronPackage{}).Count(&packageCount)
	if packageCount == 0 {
		packages := []patronModels.PatronPackage{
			{ID: "GOLD", Description: "Gold membership", PriorityOrder: 3},
			{ID: "PLATINUM", Description: "Platinum membership", PriorityOrder: 2},
			{ID: "DIAMOND", Description: "Diamond membership", PriorityOrder: 1},
		}
		if err := gormDB.Create(&packages).Error; err != nil {
			log.Printf("warning: failed to seed default patron packages: %v", err)
		}
	}

	var tierCount int64
	gormDB.Model(&patronModels.PatronTier{}).Count(&tierCount)
	if tierCount == 0 {
		tiers := []patronModels.PatronTier{
			{ID: "MONTHLY", CanExpire: true, PriorityOrder: 3},
			{ID: "ANNUAL", CanExpire: true, PriorityOrder: 2},
			{ID: "LIFETIME", CanExpire: false, PriorityOrder: 1},
		}
		if err := gormDB.Create(&tiers).Error; err != nil {
			log.Printf("warning: failed to seed default patron tiers: %v", err)
		}
	}
}
