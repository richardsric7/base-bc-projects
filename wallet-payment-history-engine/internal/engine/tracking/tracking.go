// Package tracking mirrors wallet-backend's user_wallets table into this
// engine's own TrackedWallet table - the wallet-discovery sweep described
// in PLAN.md §3.4. Unlike the original's TrackUserWallet (PLAN.md §1.2/§4
// finding 4: it deleted a wallet's row before checking whether one
// existed, so its own "update" branch could never run), this upserts in
// a single ON CONFLICT clause.
package tracking

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wallet-payment-history-engine/internal/engine/models"
)

// Sweep mirrors every wallet-backend UserWallet row into TrackedWallet,
// upserting so a re-run is a no-op for already-tracked addresses. It
// returns the addresses that were newly tracked by this call (not
// previously present in TrackedWallet) so the caller can hand them to
// the backfill watcher (PLAN.md §3.2's backfill-vs-live split).
func Sweep(db *gorm.DB) ([]models.TrackedWallet, error) {
	var wallets []models.UserWallet
	if err := db.Find(&wallets).Error; err != nil {
		return nil, fmt.Errorf("load user_wallets: %w", err)
	}
	if len(wallets) == 0 {
		return nil, nil
	}

	var existing []models.TrackedWallet
	if err := db.Find(&existing).Error; err != nil {
		return nil, fmt.Errorf("load tracked_wallets: %w", err)
	}
	alreadyTracked := make(map[string]bool, len(existing))
	for _, tw := range existing {
		alreadyTracked[tw.Address] = true
	}

	var toUpsert []models.TrackedWallet
	var newlyTracked []models.TrackedWallet
	for _, w := range wallets {
		tw := models.TrackedWallet{Address: w.Address, UserID: w.UserID}
		toUpsert = append(toUpsert, tw)
		if !alreadyTracked[w.Address] {
			newlyTracked = append(newlyTracked, tw)
		}
	}

	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "address"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id"}),
	}).Create(&toUpsert).Error; err != nil {
		return nil, fmt.Errorf("upsert tracked_wallets: %w", err)
	}

	return newlyTracked, nil
}
