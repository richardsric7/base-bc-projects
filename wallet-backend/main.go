// Command wallet-backend boots the API server: load config, open the
// database, run migrations, construct every shared dependency, and hand
// them to each component's Init function. See internal/sharedconfig for the
// GlobalConfig struct threaded through the whole boot sequence, and
// PLAN.md for the architecture this mirrors.
package main

import (
	"context"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/cache"
	announcementsModels "wallet-backend/internal/components/announcements/models"
	assetsModels "wallet-backend/internal/components/assets/models"
	paymentsModels "wallet-backend/internal/components/payments/models"
	usersModels "wallet-backend/internal/components/users/models"

	announcementsControllers "wallet-backend/internal/components/announcements/controllers"
	assetsControllers "wallet-backend/internal/components/assets/controllers"
	authControllers "wallet-backend/internal/components/auth/controllers"
	callbacksControllers "wallet-backend/internal/components/callbacks/controllers"
	paymentsControllers "wallet-backend/internal/components/payments/controllers"
	ratesControllers "wallet-backend/internal/components/rates/controllers"
	rootControllers "wallet-backend/internal/components/root/controllers"
	sharedaccessControllers "wallet-backend/internal/components/sharedaccess/controllers"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	swapsControllers "wallet-backend/internal/components/swaps/controllers"
	usersControllers "wallet-backend/internal/components/users/controllers"

	"wallet-backend/internal/db"
	"wallet-backend/internal/kyc"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/network"
	"wallet-backend/internal/notify"
	"wallet-backend/internal/rates"
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

	if env.DBAutoMigrate {
		if err := db.MigrateDB(gormDB, allModels()...); err != nil {
			log.Fatalf("failed to migrate database: %v", err)
		}
	}

	seedSecurityQuestions(gormDB)
	seedReservedNames(gormDB)

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

	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{
		// Example fixtures - replace with a live provider or DB-backed
		// table for production use.
		"USD/ETH": decimal.NewFromFloat(0.00028),
		"ETH/USD": decimal.NewFromFloat(3500),
	})

	gc := &sharedconfig.GlobalConfig{
		DB:         gormDB,
		Cache:      appCache,
		Blockchain: blockchain,
		ChainID:    env.BaseChainID,

		Mailer:  mailer,
		SMS:     notify.NewConsoleSMSProvider(),
		Push:    notify.NewConsolePushProvider(),
		Storage: blobStorage,
		KYC:     kyc.NewManualKYCProvider(),
		Fiat:    nil, // no default fiat processor; wire one in per internal/fiat's doc comment.
		Rates:   ratesProvider,
		Alerts:  alerts,

		JWTSecret:    env.JWTSecret,
		JWTExpiry:    durationFromMinutes(env.JWTExpiryMinutes),
		SIWEDomain:   env.SIWEDomain,
		GroupKeySalt: env.GroupKeySalt,
		Organisation: env.Organisation,

		RecoveryAuthoritySalt: env.RecoveryAuthoritySalt,
		RecoveryOTPTTL:        durationFromMinutes(env.RecoveryOTPTTLMinutes),
	}

	router := gin.Default()
	router.Use(middleware.CORS())
	router.Static(env.StorageURL, env.StorageDir)

	rootControllers.Init(router, gc)
	authControllers.Init(router, gc)
	usersControllers.Init(router, gc)
	assetsControllers.Init(router, gc)
	paymentsControllers.Init(router, gc)
	swapsControllers.Init(router, gc)
	sharedaccessControllers.Init(router, gc)
	ratesControllers.Init(router, gc)
	announcementsControllers.Init(router, gc)
	callbacksControllers.Init(router, gc)

	log.Printf("%s listening on :%s", env.Organisation, env.Port)
	if err := router.Run(":" + env.Port); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}

func durationFromMinutes(minutes int) time.Duration {
	return time.Duration(minutes) * time.Minute
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
