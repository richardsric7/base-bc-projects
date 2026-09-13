package tracking

import (
	"testing"

	"wallet-payment-history-engine/internal/db"
	"wallet-payment-history-engine/internal/engine/models"
)

// TestSweep_UpsertsAndReportsOnlyNew exercises the real bug this package
// fixes (PLAN.md §1.2/§4 finding 4): the original's TrackUserWallet
// deleted before checking existence, so re-running it was always a
// delete-and-recreate, never an update. Here, sweeping the same
// UserWallet row twice must upsert in place (same UserID stays), and
// report it as "newly tracked" only on the first sweep.
func TestSweep_UpsertsAndReportsOnlyNew(t *testing.T) {
	gdb, err := db.OpenDB("sqlite", t.TempDir()+"/tracking_test.sqlite")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// Test-only: create the mirrored tables directly to stand in for
	// wallet-backend's own migrations, which this engine never runs
	// itself (see internal/db.MigrateDB's doc comment).
	if err := gdb.AutoMigrate(&models.UserWallet{}, &models.CuratedToken{}); err != nil {
		t.Fatalf("migrate mirror tables: %v", err)
	}
	if err := db.MigrateDB(gdb, models.Models...); err != nil {
		t.Fatalf("migrate owned tables: %v", err)
	}

	wallet := models.UserWallet{UserID: 7, Address: "0x1111111111111111111111111111111111111111"}
	if err := gdb.Create(&wallet).Error; err != nil {
		t.Fatalf("seed user_wallets: %v", err)
	}

	newlyTracked, err := Sweep(gdb)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if len(newlyTracked) != 1 || newlyTracked[0].Address != wallet.Address {
		t.Fatalf("expected exactly the seeded wallet reported as newly tracked, got %+v", newlyTracked)
	}

	newlyTracked, err = Sweep(gdb)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(newlyTracked) != 0 {
		t.Fatalf("expected no newly tracked wallets on a re-sweep of an already-tracked wallet, got %+v", newlyTracked)
	}

	var tw models.TrackedWallet
	if err := gdb.Where("address = ?", wallet.Address).First(&tw).Error; err != nil {
		t.Fatalf("expected a single upserted TrackedWallet row: %v", err)
	}
	if tw.UserID != wallet.UserID {
		t.Fatalf("expected UserID %d, got %d", wallet.UserID, tw.UserID)
	}
}
