package main

import (
	"testing"

	"gorm.io/gorm"

	"wallet-payment-history-engine/internal/db"
	"wallet-payment-history-engine/internal/engine/indexer"
	"wallet-payment-history-engine/internal/engine/models"
	"wallet-payment-history-engine/internal/notify"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := db.OpenDB("sqlite", t.TempDir()+"/test.sqlite")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.MigrateDB(gdb, models.Models...); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return gdb
}

// TestSavePaymentHistory_Idempotent verifies the one property the whole
// crash-safe restart design leans on (PLAN.md §3.2): saving the same
// (TransactionHash, LogIndex) twice inserts exactly one row.
func TestSavePaymentHistory_Idempotent(t *testing.T) {
	gdb := openTestDB(t)
	push := notify.NewNoopProvider()

	row := models.PaymentHistory{
		TransactionHash: "0xabc",
		LogIndex:        0,
		BlockNumber:     100,
		TransactionType: indexer.TransactionTypePayment,
		From:            "0x1111111111111111111111111111111111111111",
		To:              "0x2222222222222222222222222222222222222222",
		AssetCode:       "ETH",
		Amount:          "1000000000000000000",
	}

	if err := indexer.SavePaymentHistory(gdb, row, push, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := indexer.SavePaymentHistory(gdb, row, push, nil); err != nil {
		t.Fatalf("second save (should be a no-op, not an error): %v", err)
	}

	var count int64
	if err := gdb.Model(&models.PaymentHistory{}).Where("transaction_hash = ? AND log_index = ?", row.TransactionHash, row.LogIndex).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after saving the same event twice, got %d", count)
	}
}

// TestSavePaymentHistory_DistinctLogIndexSameTx verifies two different
// events in the same transaction (e.g. a swap's two Transfer logs) are
// both kept - the unique key is (TransactionHash, LogIndex), not
// TransactionHash alone.
func TestSavePaymentHistory_DistinctLogIndexSameTx(t *testing.T) {
	gdb := openTestDB(t)
	push := notify.NewNoopProvider()

	base := models.PaymentHistory{
		TransactionHash: "0xdef",
		BlockNumber:     200,
		TransactionType: indexer.TransactionTypePayment,
		From:            "0x1111111111111111111111111111111111111111",
		To:              "0x2222222222222222222222222222222222222222",
		AssetCode:       "USDC",
		Amount:          "500",
	}
	row1 := base
	row1.LogIndex = 0
	row2 := base
	row2.LogIndex = 1

	if err := indexer.SavePaymentHistory(gdb, row1, push, nil); err != nil {
		t.Fatalf("save row1: %v", err)
	}
	if err := indexer.SavePaymentHistory(gdb, row2, push, nil); err != nil {
		t.Fatalf("save row2: %v", err)
	}

	var count int64
	if err := gdb.Model(&models.PaymentHistory{}).Where("transaction_hash = ?", base.TransactionHash).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 distinct rows for the same tx hash with different log indexes, got %d", count)
	}
}
