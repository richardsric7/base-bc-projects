// Package indexer is the one place PaymentHistory rows get written -
// SavePaymentHistory is the single canonical writer both the live watcher
// and the backfill watcher land on (PLAN.md §3.2), closing off the kind
// of drift PLAN.md §1.1 found between the two Stellar-era repos' own
// disagreeing schemas.
package indexer

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wallet-payment-history-engine/internal/engine/models"
	"wallet-payment-history-engine/internal/notify"
)

// Transaction classification labels - see PLAN.md §3.3.
const (
	TransactionTypePayment = "PAYMENT"
	TransactionTypeMint    = "MINT TOKEN"
	TransactionTypeBurn    = "BURN TOKEN"
)

// SavePaymentHistory inserts row if (TransactionHash, LogIndex) hasn't been
// recorded yet, and is a no-op otherwise - the idempotency that makes it
// safe to re-process the same block range after a restart (PLAN.md §3.2).
// On an actual insert, it notifies whichever tracked side(s) of the
// transfer are known (PLAN.md §3.5); trackedUserIDs maps a lowercased
// address to the wallet-backend user ID that owns it, for whichever sides
// are tracked.
func SavePaymentHistory(db *gorm.DB, row models.PaymentHistory, push notify.Provider, trackedUserIDs map[string]uint) error {
	if row.ID == "" {
		row.ID = uuid.NewString()
	}

	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "transaction_hash"}, {Name: "log_index"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return fmt.Errorf("save payment history %s/%d: %w", row.TransactionHash, row.LogIndex, result.Error)
	}
	if result.RowsAffected == 0 {
		return nil // already recorded - not a new event, no notification
	}

	notifySide(push, trackedUserIDs, row, row.From, "SENT", row.To)
	notifySide(push, trackedUserIDs, row, row.To, "RECEIVED", row.From)
	return nil
}

func notifySide(push notify.Provider, trackedUserIDs map[string]uint, row models.PaymentHistory, side, direction, counterAddress string) {
	userID, ok := trackedUserIDs[side]
	if !ok {
		return
	}
	_ = push.NotifyPayment(notify.Event{
		UserID:          userID,
		Address:         side,
		Direction:       direction,
		TransactionType: row.TransactionType,
		Amount:          row.Amount,
		AssetCode:       row.AssetCode,
		CounterAddress:  counterAddress,
		TransactionHash: row.TransactionHash,
	})
}
