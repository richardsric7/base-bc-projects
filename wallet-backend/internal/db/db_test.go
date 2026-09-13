package db

import (
	"testing"
	"time"
)

func TestSetPoolLimitsAndPoolStats(t *testing.T) {
	gormDB, err := OpenDB("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	if err := SetPoolLimits(gormDB, 7, 2, 5*time.Minute); err != nil {
		t.Fatalf("SetPoolLimits: %v", err)
	}

	stats, err := PoolStats(gormDB)
	if err != nil {
		t.Fatalf("PoolStats: %v", err)
	}
	if stats.MaxOpenConnections != 7 {
		t.Fatalf("expected MaxOpenConnections to be 7, got %d", stats.MaxOpenConnections)
	}
}
