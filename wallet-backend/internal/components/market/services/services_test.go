package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/market/models"
	usersModels "wallet-backend/internal/components/users/models"
)

const (
	baseToken  = "0x0000000000000000000000000000000000000b"
	quoteToken = "0x0000000000000000000000000000000000000c"
)

// fakeBlockchain is a BlockchainClient that never touches the network,
// recording every transferFrom call - same pattern as sharedaccess/fiat's
// tests. failFromAddress, if set, makes any call whose "from" argument
// (recovered from the encoded calldata) matches it fail, to exercise the
// failed-settlement path.
type fakeBlockchain struct {
	calls        []call
	failFromAddr string
}

type call struct {
	to   common.Address
	data []byte
}

func (f *fakeBlockchain) SignAndSubmitTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, _ *big.Int, data []byte, _ *uint64) (string, error) {
	// transferFrom(address,address,uint256): 4-byte selector + 3x32-byte args.
	if len(data) >= 36 && f.failFromAddr != "" {
		from := common.BytesToAddress(data[4:36])
		if from == common.HexToAddress(f.failFromAddr) {
			return "", apperrors.Internal("simulated transferFrom failure")
		}
	}
	f.calls = append(f.calls, call{to: *to, data: data})
	return "0xhash", nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate market models: %v", err)
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

func seedCuratedTokens(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&assetsModels.CuratedToken{Symbol: "BASE", ContractAddress: baseToken, Decimals: 18}).Error; err != nil {
		t.Fatalf("seed base token: %v", err)
	}
	if err := db.Create(&assetsModels.CuratedToken{Symbol: "QUOTE", ContractAddress: quoteToken, Decimals: 6}).Error; err != nil {
		t.Fatalf("seed quote token: %v", err)
	}
}

func newTestService(t *testing.T, blockchain *fakeBlockchain) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	seedCuratedTokens(t, db)
	svc := New(db, blockchain, "test-escrow-salt")
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

func TestPlaceOffer_ValidatesInput(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	user := createTestUser(t, db, "alice")

	cases := []struct {
		name       string
		offerType  models.OfferType
		base       string
		quote      string
		price      string
		qty        string
		wantStatus int
	}{
		{"bad offer type", "HOLD", baseToken, quoteToken, "1", "1", 400},
		{"zero price", models.OfferSell, baseToken, quoteToken, "0", "1", 400},
		{"negative quantity", models.OfferSell, baseToken, quoteToken, "1", "-1", 400},
		{"same token both sides", models.OfferSell, baseToken, baseToken, "1", "1", 400},
		{"native eth rejected", models.OfferSell, "", quoteToken, "1", "1", 400},
		{"uncurated token", models.OfferSell, "0x00000000000000000000000000000000000099", quoteToken, "1", "1", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.PlaceOffer(user.Address, tc.offerType, tc.base, tc.quote, tc.price, tc.qty)
			if err == nil {
				t.Fatal("expected an error")
			}
			if status := appErrStatus(t, err); status != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, status)
			}
		})
	}
}

func TestPlaceOffer_FullFillAtRestingPrice(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain)
	seller := createTestUser(t, db, "bob")
	buyer := createTestUser(t, db, "carol")

	sellOffer, err := svc.PlaceOffer(seller.Address, models.OfferSell, baseToken, quoteToken, "10", "5")
	if err != nil {
		t.Fatalf("PlaceOffer (sell) returned error: %v", err)
	}
	// Buyer is willing to pay up to 12, but the resting sell is priced at
	// 10 - the trade must execute at the resting (maker) price, 10.
	buyOffer, err := svc.PlaceOffer(buyer.Address, models.OfferBuy, baseToken, quoteToken, "12", "5")
	if err != nil {
		t.Fatalf("PlaceOffer (buy) returned error: %v", err)
	}

	if buyOffer.Status != models.OfferFilled || buyOffer.RemainingQuantity != "0" {
		t.Fatalf("expected buy offer fully filled, got %+v", buyOffer)
	}
	var reloadedSell models.MarketOffer
	db.First(&reloadedSell, "id = ?", sellOffer.ID)
	if reloadedSell.Status != models.OfferFilled || reloadedSell.RemainingQuantity != "0" {
		t.Fatalf("expected sell offer fully filled, got %+v", reloadedSell)
	}

	var trades []models.MarketTrade
	db.Find(&trades)
	if len(trades) != 1 || trades[0].Price != "10" || trades[0].Quantity != "5" {
		t.Fatalf("unexpected trades: %+v", trades)
	}
	if len(blockchain.calls) != 2 {
		t.Fatalf("expected two transferFrom calls (base leg + quote leg), got %d", len(blockchain.calls))
	}
}

func TestPlaceOffer_PartialFill(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seller := createTestUser(t, db, "dave")
	buyer := createTestUser(t, db, "erin")

	sellOffer, err := svc.PlaceOffer(seller.Address, models.OfferSell, baseToken, quoteToken, "10", "10")
	if err != nil {
		t.Fatalf("PlaceOffer (sell) returned error: %v", err)
	}
	buyOffer, err := svc.PlaceOffer(buyer.Address, models.OfferBuy, baseToken, quoteToken, "10", "4")
	if err != nil {
		t.Fatalf("PlaceOffer (buy) returned error: %v", err)
	}

	if buyOffer.Status != models.OfferFilled {
		t.Fatalf("expected buy offer fully filled, got %+v", buyOffer)
	}
	var reloadedSell models.MarketOffer
	db.First(&reloadedSell, "id = ?", sellOffer.ID)
	if reloadedSell.Status != models.OfferPartiallyFilled || reloadedSell.RemainingQuantity != "6" {
		t.Fatalf("expected sell offer partially filled with 6 remaining, got %+v", reloadedSell)
	}
}

func TestPlaceOffer_PriceTimePriority(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	sellerHigh := createTestUser(t, db, "frank")
	sellerLow := createTestUser(t, db, "grace")
	buyer := createTestUser(t, db, "heidi")

	if _, err := svc.PlaceOffer(sellerHigh.Address, models.OfferSell, baseToken, quoteToken, "12", "5"); err != nil {
		t.Fatalf("PlaceOffer (high sell) returned error: %v", err)
	}
	lowSell, err := svc.PlaceOffer(sellerLow.Address, models.OfferSell, baseToken, quoteToken, "10", "5")
	if err != nil {
		t.Fatalf("PlaceOffer (low sell) returned error: %v", err)
	}

	buyOffer, err := svc.PlaceOffer(buyer.Address, models.OfferBuy, baseToken, quoteToken, "12", "5")
	if err != nil {
		t.Fatalf("PlaceOffer (buy) returned error: %v", err)
	}
	if buyOffer.Status != models.OfferFilled {
		t.Fatalf("expected buy offer filled, got %+v", buyOffer)
	}

	var trades []models.MarketTrade
	db.Find(&trades)
	if len(trades) != 1 || trades[0].SellOfferID != lowSell.ID || trades[0].Price != "10" {
		t.Fatalf("expected the match to prefer the better (lower) price, got %+v", trades)
	}
}

func TestPlaceOffer_FailedSettlementCancelsRestingOffer(t *testing.T) {
	db := newTestDB(t)
	seedCuratedTokens(t, db)
	sellerUser := createTestUser(t, db, "ivan")
	buyerUser := createTestUser(t, db, "judy")

	blockchain := &fakeBlockchain{failFromAddr: sellerUser.Address}
	svc := New(db, blockchain, "test-escrow-salt")

	sellOffer, err := svc.PlaceOffer(sellerUser.Address, models.OfferSell, baseToken, quoteToken, "10", "5")
	if err != nil {
		t.Fatalf("PlaceOffer (sell) returned error: %v", err)
	}
	buyOffer, err := svc.PlaceOffer(buyerUser.Address, models.OfferBuy, baseToken, quoteToken, "10", "5")
	if err != nil {
		t.Fatalf("PlaceOffer (buy) returned error: %v", err)
	}

	// The seller's leg fails (simulating a missing/insufficient approve) -
	// the seller's offer should be canceled and the buy offer left open
	// (no compatible counter-offer remains) rather than the engine looping
	// forever on the same failing match.
	var reloadedSell models.MarketOffer
	db.First(&reloadedSell, "id = ?", sellOffer.ID)
	if reloadedSell.Status != models.OfferCanceled {
		t.Fatalf("expected the failing seller's offer to be canceled, got %+v", reloadedSell)
	}
	var reloadedBuy models.MarketOffer
	db.First(&reloadedBuy, "id = ?", buyOffer.ID)
	if reloadedBuy.Status != models.OfferOpen {
		t.Fatalf("expected the buy offer to remain open, got %+v", reloadedBuy)
	}

	var tradeCount int64
	db.Model(&models.MarketTrade{}).Count(&tradeCount)
	if tradeCount != 0 {
		t.Fatalf("expected no trade to be recorded, got %d", tradeCount)
	}
}

func TestCancelOffer(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	user := createTestUser(t, db, "kevin")
	other := createTestUser(t, db, "laura")

	offer, err := svc.PlaceOffer(user.Address, models.OfferSell, baseToken, quoteToken, "10", "5")
	if err != nil {
		t.Fatalf("PlaceOffer returned error: %v", err)
	}

	if _, err := svc.CancelOffer(other.Address, offer.ID); err == nil {
		t.Fatal("expected an error when a non-owner cancels an offer")
	} else if status := appErrStatus(t, err); status != 403 {
		t.Fatalf("expected a 403 AppError, got %d", status)
	}

	canceled, err := svc.CancelOffer(user.Address, offer.ID)
	if err != nil {
		t.Fatalf("CancelOffer returned error: %v", err)
	}
	if canceled.Status != models.OfferCanceled {
		t.Fatalf("expected offer canceled, got %+v", canceled)
	}

	if _, err := svc.CancelOffer(user.Address, offer.ID); err == nil {
		t.Fatal("expected an error canceling an already-canceled offer")
	} else if status := appErrStatus(t, err); status != 409 {
		t.Fatalf("expected a 409 AppError, got %d", status)
	}
}

func TestListOrderBookAndMyOffers(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	user := createTestUser(t, db, "mallory")

	if _, err := svc.PlaceOffer(user.Address, models.OfferSell, baseToken, quoteToken, "10", "5"); err != nil {
		t.Fatalf("PlaceOffer returned error: %v", err)
	}

	buys, sells, err := svc.ListOrderBook(baseToken, quoteToken)
	if err != nil {
		t.Fatalf("ListOrderBook returned error: %v", err)
	}
	if len(buys) != 0 || len(sells) != 1 {
		t.Fatalf("unexpected order book: buys=%d sells=%d", len(buys), len(sells))
	}

	myOffers, err := svc.ListMyOffers(user.Address)
	if err != nil {
		t.Fatalf("ListMyOffers returned error: %v", err)
	}
	if len(myOffers) != 1 {
		t.Fatalf("expected one offer, got %d", len(myOffers))
	}
}
