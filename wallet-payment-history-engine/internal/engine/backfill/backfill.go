// Package backfill reconstructs a newly tracked wallet's full payment
// history once, before the live watcher's cursor-based polling takes
// over the going-forward tail - the real, working equivalent of the
// original's MonitorPublicKeyPaymentStream/TrackUserWallet backfill-vs-
// live split (PLAN.md §1.3, §3.2), on infrastructure that actually works
// (chunked eth_getLogs block-range scans, not a per-account SSE stream
// Base doesn't offer).
package backfill

import (
	"context"
	"fmt"
	"log"

	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"

	"wallet-payment-history-engine/internal/alerting"
	"wallet-payment-history-engine/internal/engine/indexer"
	"wallet-payment-history-engine/internal/engine/models"
	"wallet-payment-history-engine/internal/network"
	"wallet-payment-history-engine/internal/notify"
)

// chunkSize bounds how many blocks one eth_getLogs / eth_getBlockByNumber
// pass covers, so scanning from a low floor block to the current tip
// doesn't attempt one unbounded RPC range.
const chunkSize = 2000

// Wallets reconstructs full history for each of newlyTracked, from
// floorBlock (PLAN.md §3.2's "no pre-existence to backfill before this
// point" configurable floor - 0 means genesis) through the current chain
// tip. It never touches IndexerCursor - that's the live watcher's own
// resume position, unaffected by a per-wallet backfill running alongside
// it (a block already covered by backfill is simply re-seen and no-opped
// by SavePaymentHistory's idempotent insert once the live watcher's
// cursor reaches it).
func Wallets(ctx context.Context, db *gorm.DB, client *network.Client, push notify.Provider, alerts alerting.Notifier, floorBlock uint64, newlyTracked []models.TrackedWallet) {
	if len(newlyTracked) == 0 {
		return
	}

	tip, err := client.BlockNumber(ctx)
	if err != nil {
		log.Printf("[backfill] failed to fetch chain tip: %v", err)
		_ = alerts.Notify(fmt.Sprintf("payment-history-engine backfill error: %v", err))
		return
	}

	curatedTokens, err := indexer.LoadCuratedTokens(db)
	if err != nil {
		log.Printf("[backfill] failed to load curated tokens: %v", err)
		_ = alerts.Notify(fmt.Sprintf("payment-history-engine backfill error: %v", err))
		return
	}

	for _, wallet := range newlyTracked {
		if err := backfillOneRecovered(ctx, db, client, push, curatedTokens, floorBlock, tip, wallet); err != nil {
			log.Printf("[backfill] wallet %s failed: %v", wallet.Address, err)
			_ = alerts.Notify(fmt.Sprintf("payment-history-engine backfill error for %s: %v", wallet.Address, err))
		}
	}
}

func backfillOneRecovered(ctx context.Context, db *gorm.DB, client *network.Client, push notify.Provider, curatedTokens []models.CuratedToken, floorBlock, tip uint64, wallet models.TrackedWallet) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()
	return backfillOne(ctx, db, client, push, curatedTokens, floorBlock, tip, wallet)
}

func backfillOne(ctx context.Context, db *gorm.DB, client *network.Client, push notify.Provider, curatedTokens []models.CuratedToken, floorBlock, tip uint64, wallet models.TrackedWallet) error {
	address := common.HexToAddress(wallet.Address)
	addresses := []common.Address{address}
	userIDs := map[string]uint{address.Hex(): wallet.UserID}

	written := 0
	for from := floorBlock; from <= tip; from += chunkSize {
		to := from + chunkSize - 1
		if to > tip {
			to = tip
		}

		rows, err := indexer.BuildRowsForRange(ctx, client, from, to, curatedTokens, addresses)
		if err != nil {
			return fmt.Errorf("build rows for range [%d,%d]: %w", from, to, err)
		}
		for _, row := range rows {
			if err := indexer.SavePaymentHistory(db, row, push, userIDs); err != nil {
				return err
			}
		}
		written += len(rows)
	}

	log.Printf("[backfill] wallet %s: reconstructed %d row(s) from block %d to %d", wallet.Address, written, floorBlock, tip)
	return nil
}
