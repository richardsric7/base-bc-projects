// Command wallet-payment-history-engine is a headless indexer - no HTTP
// server at all (PLAN.md §3.1), matching the original engine's true
// architecture despite its vestigial, never-wired-up Gin middleware
// files. It boots the shared database, a Base RPC client, and two
// background loops: a wallet-tracking sweep (PLAN.md §3.4) and the live
// payment indexer (PLAN.md §3.2), then blocks forever.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"

	"wallet-payment-history-engine/internal/alerting"
	"wallet-payment-history-engine/internal/db"
	"wallet-payment-history-engine/internal/engine/backfill"
	"wallet-payment-history-engine/internal/engine/indexer"
	"wallet-payment-history-engine/internal/engine/models"
	"wallet-payment-history-engine/internal/engine/tracking"
	"wallet-payment-history-engine/internal/network"
	"wallet-payment-history-engine/internal/notify"
	"wallet-payment-history-engine/internal/sharedconfig"
)

// trackingSweepInterval is how often this engine polls wallet-backend's
// own user_wallets table for newly registered wallets (PLAN.md §3.4).
const trackingSweepInterval = 30 * time.Second

func main() {
	env := sharedconfig.LoadEnv()
	ctx := context.Background()

	gormDB, err := db.OpenDB(env.DBType, env.DBConnectionString)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	if env.DBAutoMigrate {
		// Only this engine's own three tables - never wallet-backend's
		// (see internal/db.MigrateDB's doc comment and engine/models/mirror.go).
		if err := db.MigrateDB(gormDB, models.Models...); err != nil {
			log.Fatalf("migrate database: %v", err)
		}
	}

	client, err := network.NewClient(ctx, env.BaseRPCURL, env.BaseChainID)
	if err != nil {
		log.Fatalf("connect to base rpc: %v", err)
	}

	var alerts alerting.Notifier = alerting.NewNoopNotifier()
	if env.DiscordWebhookURL != "" {
		alerts = alerting.NewDiscordWebhookNotifier(env.DiscordWebhookURL)
	}
	// Wiring a real notify.Provider (push, webhook, or a callback into
	// wallet-backend) is a deliberate later decision, not assumed here -
	// see PLAN.md §3.5's "planned, not required for v1" tag.
	push := notify.NewNoopProvider()

	log.Printf("wallet-payment-history-engine starting: chain_id=%d rpc=%s poll=%ds", env.BaseChainID, env.BaseRPCURL, env.PollIntervalSeconds)

	go runTrackingLoop(ctx, gormDB, client, push, alerts, env.BackfillFloorBlock)

	indexer.Run(ctx, gormDB, client, push, alerts, time.Duration(env.PollIntervalSeconds)*time.Second)
}

// runTrackingLoop periodically mirrors wallet-backend's user_wallets into
// TrackedWallet and kicks off a one-shot backfill for anything newly
// tracked (PLAN.md §3.2's backfill-vs-live split).
func runTrackingLoop(ctx context.Context, gormDB *gorm.DB, client *network.Client, push notify.Provider, alerts alerting.Notifier, floorBlock uint64) {
	for {
		newlyTracked, err := tracking.Sweep(gormDB)
		if err != nil {
			log.Printf("[tracking] sweep failed: %v", err)
			_ = alerts.Notify(fmt.Sprintf("payment-history-engine tracking sweep error: %v", err))
		} else if len(newlyTracked) > 0 {
			log.Printf("[tracking] %d newly tracked wallet(s), starting backfill", len(newlyTracked))
			backfill.Wallets(ctx, gormDB, client, push, alerts, floorBlock, newlyTracked)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(trackingSweepInterval):
		}
	}
}
