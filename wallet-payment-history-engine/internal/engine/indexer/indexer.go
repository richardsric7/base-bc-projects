package indexer

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"

	"wallet-payment-history-engine/internal/alerting"
	"wallet-payment-history-engine/internal/engine/models"
	"wallet-payment-history-engine/internal/network"
	"wallet-payment-history-engine/internal/notify"
)

// maxBlocksPerPoll caps how many blocks one RunOnce call scans, so a long
// gap since the last saved cursor (a restart after extended downtime)
// catches up in bounded chunks instead of one unbounded eth_getLogs call.
const maxBlocksPerPoll = 2000

// LoadTrackedWallets returns every tracked address plus a lowercased-
// address -> UserID map used for notification attribution.
func LoadTrackedWallets(db *gorm.DB) ([]common.Address, map[string]uint, error) {
	var wallets []models.TrackedWallet
	if err := db.Find(&wallets).Error; err != nil {
		return nil, nil, fmt.Errorf("load tracked wallets: %w", err)
	}
	addresses := make([]common.Address, 0, len(wallets))
	userIDs := make(map[string]uint, len(wallets))
	for _, w := range wallets {
		addr := common.HexToAddress(w.Address)
		addresses = append(addresses, addr)
		userIDs[addr.Hex()] = w.UserID
	}
	return addresses, userIDs, nil
}

// LoadCuratedTokens returns every active curated token.
func LoadCuratedTokens(db *gorm.DB) ([]models.CuratedToken, error) {
	var tokens []models.CuratedToken
	if err := db.Where("is_active = ?", true).Find(&tokens).Error; err != nil {
		return nil, fmt.Errorf("load curated tokens: %w", err)
	}
	return tokens, nil
}

func loadCursor(db *gorm.DB) (uint64, error) {
	var cursor models.IndexerCursor
	err := db.FirstOrCreate(&cursor, models.IndexerCursor{ID: 1}).Error
	if err != nil {
		return 0, fmt.Errorf("load indexer cursor: %w", err)
	}
	return cursor.LastBlock, nil
}

// saveCursor persists the last block scanned - done for real, after every
// successfully processed range, fixing PLAN.md §1.2/§4 finding 1 (the
// original wired this up but never called it).
func saveCursor(db *gorm.DB, lastBlock uint64) error {
	return db.Model(&models.IndexerCursor{}).Where("id = ?", 1).Update("last_block", lastBlock).Error
}

// RunOnce scans one bounded block range - from the persisted cursor (or
// the chain tip minus one, on first boot) up to the current tip - and
// persists whatever PaymentHistory rows it finds. It returns the number
// of rows written.
func RunOnce(ctx context.Context, db *gorm.DB, client *network.Client, push notify.Provider) (int, error) {
	tip, err := client.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch chain tip: %w", err)
	}

	fromBlock, err := loadCursor(db)
	if err != nil {
		return 0, err
	}
	if fromBlock == 0 {
		fromBlock = tip // first boot: start from the live tip, no historical backfill here (that's engine/backfill's job per newly tracked wallet)
	} else {
		fromBlock++ // resume just after the last block we already processed
	}
	if fromBlock > tip {
		return 0, nil // nothing new since last poll
	}

	toBlock := tip
	if toBlock-fromBlock+1 > maxBlocksPerPoll {
		toBlock = fromBlock + maxBlocksPerPoll - 1
	}

	trackedAddresses, userIDs, err := LoadTrackedWallets(db)
	if err != nil {
		return 0, err
	}
	curatedTokens, err := LoadCuratedTokens(db)
	if err != nil {
		return 0, err
	}

	rows, err := BuildRowsForRange(ctx, client, fromBlock, toBlock, curatedTokens, trackedAddresses)
	if err != nil {
		return 0, fmt.Errorf("build rows for range [%d,%d]: %w", fromBlock, toBlock, err)
	}

	for _, row := range rows {
		if err := SavePaymentHistory(db, row, push, userIDs); err != nil {
			return 0, err
		}
	}

	if err := saveCursor(db, toBlock); err != nil {
		return 0, fmt.Errorf("persist cursor at %d: %w", toBlock, err)
	}

	return len(rows), nil
}

// Run polls RunOnce forever at pollInterval, with exponential backoff and
// jitter on error and a per-iteration panic recovery - fixing PLAN.md
// §1.2/§4 finding 5 (the original's outer loop was a bare fixed 5s retry
// with no backoff, jitter, or panic recovery).
func Run(ctx context.Context, db *gorm.DB, client *network.Client, push notify.Provider, alerts alerting.Notifier, pollInterval time.Duration) {
	backoff := pollInterval
	const maxBackoff = 2 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		written, err := runOnceRecovered(ctx, db, client, push)
		if err != nil {
			log.Printf("[indexer] poll failed: %v", err)
			_ = alerts.Notify(fmt.Sprintf("payment-history-engine indexer error: %v", err))

			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
			sleep(ctx, backoff/2+jitter)
			continue
		}

		backoff = pollInterval
		if written > 0 {
			log.Printf("[indexer] recorded %d payment history row(s)", written)
		}
		sleep(ctx, pollInterval)
	}
}

// runOnceRecovered wraps RunOnce with defer/recover so a single bad
// block/log can never kill the process (PLAN.md §3.2).
func runOnceRecovered(ctx context.Context, db *gorm.DB, client *network.Client, push notify.Provider) (written int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()
	return RunOnce(ctx, db, client, push)
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
