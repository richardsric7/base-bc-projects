package services

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"wallet-backend/internal/rates"
)

// fakeNotifier records every alert sent, without touching the network -
// same pattern as fakeBlockchain.
type fakeNotifier struct {
	messages []string
}

func (f *fakeNotifier) Notify(message string) error {
	f.messages = append(f.messages, message)
	return nil
}

func TestProcessActivation_AlertsOnDispenseFailure(t *testing.T) {
	blockchain := &fakeBlockchain{sendErr: errors.New("faucet out of gas")}
	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{"USD/ETH": decimal.NewFromFloat(0.0003)})
	svc, db := newTestService(t, blockchain, ratesProvider, "")
	seedTestActivationConfig(t, db, 50)
	createTestUser(t, db, "carol")

	notifier := &fakeNotifier{}
	svc.Alerts = notifier

	if err := svc.ProcessActivation(context.Background(), "carol", "tx-ref-1", decimal.NewFromInt(10), "USD"); err == nil {
		t.Fatalf("expected ProcessActivation to fail when the faucet send fails")
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("expected exactly one alert, got %d: %v", len(notifier.messages), notifier.messages)
	}
}

func TestCheckFaucetBalance_NoOpWithoutThreshold(t *testing.T) {
	blockchain := &fakeBlockchain{nativeBal: big.NewInt(0)}
	svc, _ := newTestService(t, blockchain, rates.NewStaticProvider(nil), "")
	notifier := &fakeNotifier{}
	svc.Alerts = notifier

	if err := svc.CheckFaucetBalance(context.Background()); err != nil {
		t.Fatalf("CheckFaucetBalance: %v", err)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("expected no alert when no threshold is configured, got %v", notifier.messages)
	}
}

func TestCheckFaucetBalance_AlertsWhenBelowThreshold(t *testing.T) {
	blockchain := &fakeBlockchain{nativeBal: big.NewInt(100)}
	svc, _ := newTestService(t, blockchain, rates.NewStaticProvider(nil), "")
	svc.FaucetLowBalanceThresholdWei = big.NewInt(1000)
	notifier := &fakeNotifier{}
	svc.Alerts = notifier

	if err := svc.CheckFaucetBalance(context.Background()); err != nil {
		t.Fatalf("CheckFaucetBalance: %v", err)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("expected exactly one low-balance alert, got %d: %v", len(notifier.messages), notifier.messages)
	}
}

func TestCheckFaucetBalance_NoAlertWhenAboveThreshold(t *testing.T) {
	blockchain := &fakeBlockchain{nativeBal: big.NewInt(10000)}
	svc, _ := newTestService(t, blockchain, rates.NewStaticProvider(nil), "")
	svc.FaucetLowBalanceThresholdWei = big.NewInt(1000)
	notifier := &fakeNotifier{}
	svc.Alerts = notifier

	if err := svc.CheckFaucetBalance(context.Background()); err != nil {
		t.Fatalf("CheckFaucetBalance: %v", err)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("expected no alert when balance is above threshold, got %v", notifier.messages)
	}
}

func TestFaucetAddress_IsDeterministic(t *testing.T) {
	svc, _ := newTestService(t, &fakeBlockchain{}, rates.NewStaticProvider(nil), "")
	addr1, err := svc.FaucetAddress()
	if err != nil {
		t.Fatalf("FaucetAddress: %v", err)
	}
	addr2, err := svc.FaucetAddress()
	if err != nil {
		t.Fatalf("FaucetAddress: %v", err)
	}
	if addr1 != addr2 {
		t.Fatalf("expected FaucetAddress to be deterministic, got %q then %q", addr1, addr2)
	}
}
