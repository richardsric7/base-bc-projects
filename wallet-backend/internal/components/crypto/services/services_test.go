package services

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/crypto/models"
	usersModels "wallet-backend/internal/components/users/models"
)

// fakeBlockchain is a BlockchainClient that never touches the network,
// recording every call - same pattern used by sharedaccess/fiat's tests.
type fakeBlockchain struct {
	signedTo   []common.Address
	signedData [][]byte
	submitted  []string
	submitErr  error
}

func (f *fakeBlockchain) SignAndSubmitTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, _ *big.Int, data []byte, _ *uint64) (string, error) {
	f.signedTo = append(f.signedTo, *to)
	f.signedData = append(f.signedData, data)
	return "0xcredithash", nil
}

func (f *fakeBlockchain) SubmitSignedTransaction(_ context.Context, rawTxHex string) (string, error) {
	if f.submitErr != nil {
		return "", f.submitErr
	}
	f.submitted = append(f.submitted, rawTxHex)
	return "0xdebithash", nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate crypto models: %v", err)
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
	address := "0x" + username + "0000000000000000000000000000000000"
	user := usersModels.User{
		Username:      username,
		Email:         username + "@example.com",
		Address:       address,
		SignerAddress: address,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

func newTestService(t *testing.T, blockchain *fakeBlockchain, serverURL string) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	svc := New(db, blockchain, serverURL, "test-token", "wallet-backend.test", "test-treasury-salt", 1.0)
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

func fakeOneLiquidityServer(t *testing.T, responses map[string]interface{}) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, response := range responses {
		resp := response
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(resp)
		})
	}
	return httptest.NewServer(mux)
}

func TestEnsureDepositAddresses_CreatesNewSubwallet(t *testing.T) {
	server := fakeOneLiquidityServer(t, map[string]interface{}{
		"/wallets/v1/sub": models.SubwalletResponse{
			Data: models.CryptoSubWallet{
				WalletID: "w1", Currency: "USDC",
				Addresses: []models.CryptoAddress{{Address: "0xdeposit1", Network: "base"}},
			},
		},
	})
	defer server.Close()
	svc, db := newTestService(t, &fakeBlockchain{}, server.URL)
	user := createTestUser(t, db, "alice")

	addresses, err := svc.EnsureDepositAddresses(user.Address, "USDC")
	if err != nil {
		t.Fatalf("EnsureDepositAddresses returned error: %v", err)
	}
	if len(addresses) != 1 || addresses[0].DepositAddress != "0xdeposit1" || addresses[0].Network != "base" {
		t.Fatalf("unexpected addresses: %+v", addresses)
	}
}

func TestEnsureDepositAddresses_ReusesExisting(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, "http://should-not-be-called.invalid")
	user := createTestUser(t, db, "bob")
	if err := db.Create(&models.CryptoDepositAddress{UserID: user.ID, Currency: "USDC", Network: "base", DepositAddress: "0xexisting"}).Error; err != nil {
		t.Fatalf("seed deposit address: %v", err)
	}

	addresses, err := svc.EnsureDepositAddresses(user.Address, "USDC")
	if err != nil {
		t.Fatalf("EnsureDepositAddresses returned error: %v", err)
	}
	if len(addresses) != 1 || addresses[0].DepositAddress != "0xexisting" {
		t.Fatalf("expected the existing address to be reused without an API call, got %+v", addresses)
	}
}

func TestPollNewDeposits_CreditsViaTreasuryTransfer(t *testing.T) {
	server := fakeOneLiquidityServer(t, map[string]interface{}{
		"/wallets/v1/deposit": models.DepositListResponse{
			Data: []models.DepositItem{{
				DepositID: "dep-1", TxID: "tx-1", Amount: "10.5", Currency: "USDC",
				FromAddress: "0xexternal", ToAddress: "0xdepositaddr", IsCompleted: true, IsValid: true,
			}},
		},
	})
	defer server.Close()
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain, server.URL)
	user := createTestUser(t, db, "carol")
	if err := db.Create(&models.CryptoDepositAddress{UserID: user.ID, Currency: "USDC", Network: "base", DepositAddress: "0xdepositaddr"}).Error; err != nil {
		t.Fatalf("seed deposit address: %v", err)
	}
	if err := db.Create(&assetsModels.CuratedToken{Symbol: "USDC", ContractAddress: "0x0000000000000000000000000000000000000002", Decimals: 6}).Error; err != nil {
		t.Fatalf("seed curated token: %v", err)
	}

	svc.PollNewDeposits()

	var deposit models.CryptoDeposit
	if err := db.Where("deposit_id = ?", "dep-1").First(&deposit).Error; err != nil {
		t.Fatalf("expected a recorded deposit: %v", err)
	}
	if !deposit.Credited || deposit.CreditTxHash == "" {
		t.Fatalf("expected the deposit to be credited, got %+v", deposit)
	}
	if len(blockchain.signedTo) != 1 {
		t.Fatalf("expected exactly one on-chain credit transfer, got %d", len(blockchain.signedTo))
	}
}

func TestPollNewDeposits_SkipsWithoutCuratedToken(t *testing.T) {
	server := fakeOneLiquidityServer(t, map[string]interface{}{
		"/wallets/v1/deposit": models.DepositListResponse{
			Data: []models.DepositItem{{
				DepositID: "dep-2", Amount: "5", Currency: "BTC",
				ToAddress: "0xdepositaddr2", IsCompleted: true, IsValid: true,
			}},
		},
	})
	defer server.Close()
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain, server.URL)
	user := createTestUser(t, db, "dave")
	if err := db.Create(&models.CryptoDepositAddress{UserID: user.ID, Currency: "BTC", Network: "base", DepositAddress: "0xdepositaddr2"}).Error; err != nil {
		t.Fatalf("seed deposit address: %v", err)
	}

	svc.PollNewDeposits()

	var deposit models.CryptoDeposit
	if err := db.Where("deposit_id = ?", "dep-2").First(&deposit).Error; err != nil {
		t.Fatalf("expected the deposit to still be recorded: %v", err)
	}
	if deposit.Credited {
		t.Fatal("expected crediting to be skipped without a matching curated token")
	}
	if len(blockchain.signedTo) != 0 {
		t.Fatal("expected no on-chain call without a curated token")
	}
}

func TestPollNewDeposits_SkipsAlreadyRecorded(t *testing.T) {
	server := fakeOneLiquidityServer(t, map[string]interface{}{
		"/wallets/v1/deposit": models.DepositListResponse{
			Data: []models.DepositItem{{
				DepositID: "dep-3", Amount: "1", Currency: "USDC",
				ToAddress: "0xdepositaddr3", IsCompleted: true, IsValid: true,
			}},
		},
	})
	defer server.Close()
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain, server.URL)
	if err := db.Create(&models.CryptoDeposit{UserID: 1, DepositID: "dep-3", Currency: "USDC", Amount: "1"}).Error; err != nil {
		t.Fatalf("seed existing deposit: %v", err)
	}

	svc.PollNewDeposits()

	var count int64
	db.Model(&models.CryptoDeposit{}).Where("deposit_id = ?", "dep-3").Count(&count)
	if count != 1 {
		t.Fatalf("expected exactly one deposit row (no duplicate), got %d", count)
	}
	if len(blockchain.signedTo) != 0 {
		t.Fatal("expected no on-chain call for an already-recorded deposit")
	}
}

func TestGetWithdrawalNetworks_RefreshesWhenEmpty(t *testing.T) {
	server := fakeOneLiquidityServer(t, map[string]interface{}{
		"/wallets/v1/withdrawal/networks": models.WithdrawalNetworksResponse{
			Data: []models.WithdrawalNetworkItem{{Network: "base", WithdrawMin: "1", WithdrawMax: "1000", WithdrawFee: "0.5"}},
		},
	})
	defer server.Close()
	svc, _ := newTestService(t, &fakeBlockchain{}, server.URL)

	result, err := svc.GetWithdrawalNetworks("USDC")
	if err != nil {
		t.Fatalf("GetWithdrawalNetworks returned error: %v", err)
	}
	if len(result.Networks) != 1 || result.Networks[0].Network != "base" {
		t.Fatalf("unexpected networks: %+v", result.Networks)
	}
	if result.TreasuryAddress == "" {
		t.Fatal("expected a non-empty treasury address")
	}
}

func seedWithdrawalNetwork(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&models.WithdrawalNetwork{Currency: "USDC", Network: "base", WithdrawMin: "1", WithdrawMax: "1000", WithdrawFee: "0.5"}).Error; err != nil {
		t.Fatalf("seed withdrawal network: %v", err)
	}
}

func TestRequestWithdrawal_Success(t *testing.T) {
	server := fakeOneLiquidityServer(t, map[string]interface{}{
		"/wallets/v1/withdrawal": models.WithdrawalResponse{
			Data: struct {
				WithdrawalID string `json:"withdrawalId"`
				Status       string `json:"status"`
			}{WithdrawalID: "wdl-1", Status: "processing"},
		},
	})
	defer server.Close()
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain, server.URL)
	user := createTestUser(t, db, "erin")
	seedWithdrawalNetwork(t, db)

	result, err := svc.RequestWithdrawal(user.Address, "USDC", "base", "0xexternal", 100, "0xsignedtx")
	if err != nil {
		t.Fatalf("RequestWithdrawal returned error: %v", err)
	}
	if result.Status != "processing" || result.OneLiquidityWithdrawalID != "wdl-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	// service fee 1% of 100 = 1, network fee 0.5 -> amountToWithdraw = 98.5
	if result.AmountToWithdraw != 98.5 {
		t.Fatalf("expected amountToWithdraw 98.5, got %v", result.AmountToWithdraw)
	}
	if len(blockchain.submitted) != 1 || blockchain.submitted[0] != "0xsignedtx" {
		t.Fatalf("expected the signed treasury transfer to be submitted, got %+v", blockchain.submitted)
	}
}

func TestRequestWithdrawal_RejectsBelowMinimum(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, "")
	user := createTestUser(t, db, "frank")
	seedWithdrawalNetwork(t, db)

	_, err := svc.RequestWithdrawal(user.Address, "USDC", "base", "0xexternal", 0.5, "0xsignedtx")
	if err == nil {
		t.Fatal("expected an error for an amount below the minimum")
	}
	if status := appErrStatus(t, err); status != 400 {
		t.Fatalf("expected a 400 AppError, got %d", status)
	}
}

func TestRequestWithdrawal_RejectsAboveMaximum(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, "")
	user := createTestUser(t, db, "grace")
	seedWithdrawalNetwork(t, db)

	_, err := svc.RequestWithdrawal(user.Address, "USDC", "base", "0xexternal", 5000, "0xsignedtx")
	if err == nil {
		t.Fatal("expected an error for an amount above the maximum")
	}
	if status := appErrStatus(t, err); status != 400 {
		t.Fatalf("expected a 400 AppError, got %d", status)
	}
}

func TestRequestWithdrawal_RejectsUnsupportedNetwork(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{}, "")
	user := createTestUser(t, db, "heidi")
	seedWithdrawalNetwork(t, db)

	_, err := svc.RequestWithdrawal(user.Address, "USDC", "unsupported-network", "0xexternal", 100, "0xsignedtx")
	if err == nil {
		t.Fatal("expected an error for an unsupported network")
	}
	if status := appErrStatus(t, err); status != 400 {
		t.Fatalf("expected a 400 AppError, got %d", status)
	}
}
