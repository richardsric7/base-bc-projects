package services

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/tokenization/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/network"
)

// fakeBlockchain is a BlockchainClient that never touches the network,
// recording every call - same pattern used by market/crypto/sharedaccess's
// tests. DeployContract returns a distinct fake address each call so a
// mint's issuer and sale contract addresses never collide.
type fakeBlockchain struct {
	deployCount int
	signedTo    []common.Address
	signedData  [][]byte
	submitted   []string
	balances    map[string]*big.Int
}

func (f *fakeBlockchain) DeployContract(_ context.Context, _ *ecdsa.PrivateKey, _ []byte) (string, string, error) {
	f.deployCount++
	return fmt.Sprintf("0xcontract%d000000000000000000000000000000", f.deployCount), fmt.Sprintf("0xdeploytx%d", f.deployCount), nil
}

func (f *fakeBlockchain) SignAndSubmitTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, _ *big.Int, data []byte, _ *uint64) (string, error) {
	f.signedTo = append(f.signedTo, *to)
	f.signedData = append(f.signedData, data)
	return "0xtxhash", nil
}

func (f *fakeBlockchain) SignTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, _ *big.Int, data []byte, _ *uint64) (string, error) {
	f.signedTo = append(f.signedTo, *to)
	f.signedData = append(f.signedData, data)
	return "0xrawsignedtx", nil
}

func (f *fakeBlockchain) BuildContractCallTx(_ context.Context, from, contractAddress string, value *big.Int, data []byte, _ *uint64) (*network.UnsignedTx, error) {
	return &network.UnsignedTx{To: contractAddress, Data: "0x" + common.Bytes2Hex(data)}, nil
}

func (f *fakeBlockchain) SubmitSignedTransaction(_ context.Context, rawTxHex string) (string, error) {
	f.submitted = append(f.submitted, rawTxHex)
	return "0xsubmittedhash", nil
}

func (f *fakeBlockchain) ERC20BalanceOf(_ context.Context, tokenAddress, owner string) (*big.Int, error) {
	if f.balances == nil {
		return big.NewInt(0), nil
	}
	if bal, ok := f.balances[tokenAddress+owner]; ok {
		return bal, nil
	}
	return big.NewInt(0), nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	for _, migrate := range [][]interface{}{models.Models, usersModels.Models, assetsModels.Models, sharedaccessModels.Models} {
		if err := db.AutoMigrate(migrate...); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

func newTestService(t *testing.T, blockchain *fakeBlockchain) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	svc := New(db, blockchain, nil, "test-issuer-salt", "test-distribution-salt", decimal.Zero)
	return svc, db
}

func createTestUser(t *testing.T, db *gorm.DB, username string, kycVerified bool) usersModels.User {
	t.Helper()
	level := 0
	if kycVerified {
		level = 1
	}
	address := "0x" + username + "000000000000000000000000000000000"
	user := usersModels.User{
		Username:         username,
		Email:            username + "@example.com",
		Address:          address,
		SignerAddress:    address,
		KYCVerifiedLevel: level,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

func seedCountryAndCurrencies(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&models.TokenizationCountryConfig{CountryCode: "NG"}).Error; err != nil {
		t.Fatalf("seed country config: %v", err)
	}
	for _, symbol := range []string{"USDC", "CNGN"} {
		if err := db.Create(&models.TokenizationCurrency{Symbol: symbol}).Error; err != nil {
			t.Fatalf("seed currency %s: %v", symbol, err)
		}
		if err := db.Create(&assetsModels.CuratedToken{
			Symbol:          symbol,
			ContractAddress: "0x" + symbol + "00000000000000000000000000000000000",
			Decimals:        6,
			IsActive:        true,
		}).Error; err != nil {
			t.Fatalf("seed curated token %s: %v", symbol, err)
		}
	}
}

func appErrStatus(t *testing.T, err error) int {
	t.Helper()
	appErr, ok := err.(*apperrors.AppError)
	if !ok {
		t.Fatalf("expected an *apperrors.AppError, got %#v", err)
	}
	return appErr.StatusCode()
}

func validDraftAsset() *models.TokenizedAsset {
	return &models.TokenizedAsset{
		AssetCountryLocation:             "NG",
		NumberOfTokenToBeIssued:          "1000",
		MaxNumberOfTokenAvailableForSale: "600",
		AssetCode:                        "TGLD",
		AssetName:                        "Trovo Gold",
		AssetDescription:                 "A gold-backed asset",
		AssetLogo:                        "https://example.com/logo.png",
		PricePerToken:                    "2.5",
		AssetDecimals:                    2,
		OfferingType:                     models.OfferingPublic,
		SecApprovalNumber:                "SEC-123",
	}
}
