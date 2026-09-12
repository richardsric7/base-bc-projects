package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/fiat/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/rates"
)

// fakeBlockchain is a BlockchainClient that never touches the network,
// recording every call so tests can assert on what was dispensed/submitted -
// same pattern as sharedaccess's fakeBlockchain.
type fakeBlockchain struct {
	sentTo    []common.Address
	sentValue []*big.Int
	sentData  [][]byte
	submitted []string
	submitErr error
	sendErr   error
}

func (f *fakeBlockchain) SignAndSubmitTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, _ *uint64) (string, error) {
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.sentTo = append(f.sentTo, *to)
	f.sentValue = append(f.sentValue, value)
	f.sentData = append(f.sentData, data)
	return "0xhash", nil
}

func (f *fakeBlockchain) SubmitSignedTransaction(_ context.Context, rawTxHex string) (string, error) {
	if f.submitErr != nil {
		return "", f.submitErr
	}
	f.submitted = append(f.submitted, rawTxHex)
	return "0xsubmittedhash", nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate fiat models: %v", err)
	}
	if err := db.AutoMigrate(usersModels.Models...); err != nil {
		t.Fatalf("migrate users models: %v", err)
	}
	if err := db.AutoMigrate(assetsModels.Models...); err != nil {
		t.Fatalf("migrate assets models: %v", err)
	}
	return db
}

func createTestUser(t *testing.T, db *gorm.DB, username string) usersModels.User {
	t.Helper()
	user := usersModels.User{
		Username: username,
		Email:    username + "@example.com",
		Address:  "0x" + username + "0000000000000000000000000000000000",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

func seedTestActivationConfig(t *testing.T, db *gorm.DB, rewardPercent float64) {
	t.Helper()
	if err := db.Create(&models.ActivationConfig{FiatCurrency: "USD", FiatActivationAmount: 10, RewardTokenPercent: rewardPercent}).Error; err != nil {
		t.Fatalf("seed activation config: %v", err)
	}
}

func newTestService(t *testing.T, blockchain *fakeBlockchain, ratesProvider rates.Provider, rewardTokenSymbol string) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	svc := New(db, blockchain, ratesProvider, "test-faucet-salt", rewardTokenSymbol)
	return svc, db
}

func appErrStatus(t *testing.T, err error) int {
	t.Helper()
	appErr, ok := err.(*apperrors.AppError)
	if !ok {
		t.Fatalf("expected an *apperrors.AppError, got %#v", err)
	}
	return appErr.StatusCode()
}

func TestGetActivationQuote_ReturnsConfiguredAmount(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, rates.NewStaticProvider(nil), "")
	seedTestActivationConfig(t, db, 50)
	user := createTestUser(t, db, "alice")

	quote, err := svc.GetActivationQuote(user.Address)
	if err != nil {
		t.Fatalf("GetActivationQuote returned error: %v", err)
	}
	if quote.AlreadyActivated {
		t.Fatal("expected AlreadyActivated to be false for a fresh user")
	}
	if quote.FiatAmount != 10 || quote.FiatCurrency != "USD" || quote.RewardTokenPercent != 50 || quote.GasPercent != 50 {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}

func TestGetActivationQuote_AlreadyActivated(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, rates.NewStaticProvider(nil), "")
	seedTestActivationConfig(t, db, 50)
	user := createTestUser(t, db, "bob")
	if err := db.Model(&user).Update("activated", true).Error; err != nil {
		t.Fatalf("seed activated: %v", err)
	}

	quote, err := svc.GetActivationQuote(user.Address)
	if err != nil {
		t.Fatalf("GetActivationQuote returned error: %v", err)
	}
	if !quote.AlreadyActivated || quote.FiatAmount != 0 {
		t.Fatalf("expected AlreadyActivated with zero amount, got %+v", quote)
	}
}

func TestProcessActivation_DispensesGasOnly(t *testing.T) {
	blockchain := &fakeBlockchain{}
	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{"USD/ETH": decimal.NewFromFloat(0.0003)})
	svc, db := newTestService(t, blockchain, ratesProvider, "")
	seedTestActivationConfig(t, db, 50)
	user := createTestUser(t, db, "carol")

	if err := svc.ProcessActivation(context.Background(), "carol", "tx-ref-1", decimal.NewFromInt(10), "USD"); err != nil {
		t.Fatalf("ProcessActivation returned error: %v", err)
	}

	if len(blockchain.sentTo) != 1 {
		t.Fatalf("expected exactly one dispense call (gas only), got %d", len(blockchain.sentTo))
	}

	var reloaded usersModels.User
	if err := db.Where("id = ?", user.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if !reloaded.Activated {
		t.Fatal("expected user to be marked Activated")
	}

	var paymentCount int64
	db.Model(&models.FiatPayment{}).Where("user_id = ? AND payment_type = ?", user.ID, "ACTIVATION").Count(&paymentCount)
	if paymentCount != 1 {
		t.Fatalf("expected one FiatPayment audit row, got %d", paymentCount)
	}
}

func TestProcessActivation_IsIdempotent(t *testing.T) {
	blockchain := &fakeBlockchain{}
	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{"USD/ETH": decimal.NewFromFloat(0.0003)})
	svc, db := newTestService(t, blockchain, ratesProvider, "")
	seedTestActivationConfig(t, db, 50)
	createTestUser(t, db, "dave")

	if err := svc.ProcessActivation(context.Background(), "dave", "tx-ref-1", decimal.NewFromInt(10), "USD"); err != nil {
		t.Fatalf("first ProcessActivation returned error: %v", err)
	}
	if len(blockchain.sentTo) != 1 {
		t.Fatalf("expected one dispense after first call, got %d", len(blockchain.sentTo))
	}

	// A duplicate webhook delivery for the same (or another) charge must
	// never dispense a second time.
	if err := svc.ProcessActivation(context.Background(), "dave", "tx-ref-2", decimal.NewFromInt(10), "USD"); err != nil {
		t.Fatalf("second ProcessActivation returned error: %v", err)
	}
	if len(blockchain.sentTo) != 1 {
		t.Fatalf("expected still only one dispense after a duplicate delivery, got %d", len(blockchain.sentTo))
	}
}

func TestProcessActivation_DispensesRewardTokenWhenConfigured(t *testing.T) {
	blockchain := &fakeBlockchain{}
	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{
		"USD/ETH":    decimal.NewFromFloat(0.0003),
		"USD/REWARD": decimal.NewFromFloat(2),
	})
	svc, db := newTestService(t, blockchain, ratesProvider, "REWARD")
	seedTestActivationConfig(t, db, 50)
	createTestUser(t, db, "erin")
	if err := db.Create(&assetsModels.CuratedToken{
		Symbol: "REWARD", ContractAddress: "0x00000000000000000000000000000000000001", Decimals: 18,
	}).Error; err != nil {
		t.Fatalf("seed curated token: %v", err)
	}

	if err := svc.ProcessActivation(context.Background(), "erin", "tx-ref-1", decimal.NewFromInt(10), "USD"); err != nil {
		t.Fatalf("ProcessActivation returned error: %v", err)
	}

	if len(blockchain.sentTo) != 2 {
		t.Fatalf("expected two dispense calls (gas + reward token), got %d", len(blockchain.sentTo))
	}
	if len(blockchain.sentData[1]) == 0 {
		t.Fatal("expected the second dispense to carry ERC-20 transfer calldata")
	}
}

func TestProcessActivation_SkipsRewardTokenWithoutRate(t *testing.T) {
	blockchain := &fakeBlockchain{}
	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{"USD/ETH": decimal.NewFromFloat(0.0003)})
	svc, db := newTestService(t, blockchain, ratesProvider, "REWARD")
	seedTestActivationConfig(t, db, 50)
	createTestUser(t, db, "frank")
	if err := db.Create(&assetsModels.CuratedToken{
		Symbol: "REWARD", ContractAddress: "0x00000000000000000000000000000000000001", Decimals: 18,
	}).Error; err != nil {
		t.Fatalf("seed curated token: %v", err)
	}

	if err := svc.ProcessActivation(context.Background(), "frank", "tx-ref-1", decimal.NewFromInt(10), "USD"); err != nil {
		t.Fatalf("ProcessActivation returned error: %v", err)
	}

	if len(blockchain.sentTo) != 1 {
		t.Fatalf("expected gas dispensed but reward token skipped (no rate configured), got %d calls", len(blockchain.sentTo))
	}

	var reloaded usersModels.User
	db.Where("username = ?", "frank").First(&reloaded)
	if !reloaded.Activated {
		t.Fatal("expected activation to still succeed even though the reward token was skipped")
	}
}

func TestProcessActivation_FailsWithoutGasRate(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, rates.NewStaticProvider(nil), "")
	seedTestActivationConfig(t, db, 50)
	createTestUser(t, db, "grace")

	err := svc.ProcessActivation(context.Background(), "grace", "tx-ref-1", decimal.NewFromInt(10), "USD")
	if err == nil {
		t.Fatal("expected an error when no USD/ETH rate is configured")
	}
	if status := appErrStatus(t, err); status != 500 {
		t.Fatalf("expected a 500 AppError, got %d", status)
	}
}

func TestProcessActivation_UnknownUsername(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, rates.NewStaticProvider(nil), "")
	seedTestActivationConfig(t, db, 50)

	err := svc.ProcessActivation(context.Background(), "nobody", "tx-ref-1", decimal.NewFromInt(10), "USD")
	if err == nil {
		t.Fatal("expected an error for an unknown username")
	}
	if status := appErrStatus(t, err); status != 404 {
		t.Fatalf("expected a 404 AppError, got %d", status)
	}
}

func TestCreateInvoice_RejectsDuplicateReference(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, rates.NewStaticProvider(nil), "")
	user := createTestUser(t, db, "heidi")

	if _, err := svc.CreateInvoice(user.Address, "ref-1", "flutterwave", "ACTIVATION", 10, "USD", nil); err != nil {
		t.Fatalf("first CreateInvoice returned error: %v", err)
	}
	_, err := svc.CreateInvoice(user.Address, "ref-1", "flutterwave", "ACTIVATION", 10, "USD", nil)
	if err == nil {
		t.Fatal("expected an error for a duplicate invoice reference")
	}
	if status := appErrStatus(t, err); status != 409 {
		t.Fatalf("expected a 409 AppError, got %d", status)
	}
}

func TestSettlePendingInvoice_SubmitsSignedTransaction(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain, rates.NewStaticProvider(nil), "")
	user := createTestUser(t, db, "ivan")

	signedTx := "0xdeadbeef"
	if _, err := svc.CreateInvoice(user.Address, "ref-2", "flutterwave", "ASSET_PURCHASE", 100, "USD", &signedTx); err != nil {
		t.Fatalf("CreateInvoice returned error: %v", err)
	}

	if err := svc.SettlePendingInvoice(context.Background(), "ref-2", "ref-2", 100); err != nil {
		t.Fatalf("SettlePendingInvoice returned error: %v", err)
	}

	if len(blockchain.submitted) != 1 || blockchain.submitted[0] != signedTx {
		t.Fatalf("expected the signed transaction to be submitted, got %+v", blockchain.submitted)
	}

	var invoice models.FiatPaymentInvoice
	if err := db.Where("id = ?", "ref-2").First(&invoice).Error; err != nil {
		t.Fatalf("reload invoice: %v", err)
	}
	if invoice.Status != "COMPLETED" || invoice.TransactionHash == nil {
		t.Fatalf("expected invoice to be COMPLETED with a transaction hash, got %+v", invoice)
	}
}

func TestSettlePendingInvoice_MissingInvoiceIsSoftNoOp(t *testing.T) {
	svc, _ := newTestService(t, &fakeBlockchain{}, rates.NewStaticProvider(nil), "")
	if err := svc.SettlePendingInvoice(context.Background(), "no-such-ref", "no-such-ref", 100); err != nil {
		t.Fatalf("expected a soft no-op (nil error) for a missing invoice, got %v", err)
	}
}

func TestSettlePendingInvoice_NoSignedTransactionIsSoftNoOp(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain, rates.NewStaticProvider(nil), "")
	user := createTestUser(t, db, "judy")

	if _, err := svc.CreateInvoice(user.Address, "ref-3", "flutterwave", "ASSET_PURCHASE", 100, "USD", nil); err != nil {
		t.Fatalf("CreateInvoice returned error: %v", err)
	}
	if err := svc.SettlePendingInvoice(context.Background(), "ref-3", "ref-3", 100); err != nil {
		t.Fatalf("expected a soft no-op (nil error) when there's nothing to submit, got %v", err)
	}
	if len(blockchain.submitted) != 0 {
		t.Fatal("expected nothing to be submitted")
	}
}
