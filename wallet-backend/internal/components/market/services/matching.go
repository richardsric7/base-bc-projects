package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/market/models"
	"wallet-backend/internal/network"
)

// PlaceOffer creates a resting limit order and immediately attempts to
// match it against the opposite side of the book, price-time priority
// (best price first, then oldest first at a given price). A caller must
// have already approved EscrowAddress() for at least Quantity of whatever
// asset they're offering (the base asset for a SELL, up to
// Quantity*PricePerUnit of the quote asset for a BUY) - PlaceOffer itself
// has no way to verify that in advance (an approve() only becomes visible
// on submission, and checking it here would just be a race against the
// same transferFrom call at match time), so a match's settlement failing
// with insufficient allowance is a real, expected outcome handled by
// canceling the under-funded side rather than blocking the whole engine -
// see settleMatch.
func (s *Service) PlaceOffer(address string, offerType models.OfferType, baseToken, quoteToken, pricePerUnit, quantity string) (*models.MarketOffer, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}

	if offerType != models.OfferBuy && offerType != models.OfferSell {
		return nil, apperrors.BadRequest(`offerType must be "BUY" or "SELL"`)
	}
	price, err := decimal.NewFromString(pricePerUnit)
	if err != nil || !price.IsPositive() {
		return nil, apperrors.BadRequest("pricePerUnit must be a positive decimal")
	}
	qty, err := decimal.NewFromString(quantity)
	if err != nil || !qty.IsPositive() {
		return nil, apperrors.BadRequest("quantity must be a positive decimal")
	}
	if baseToken == quoteToken {
		return nil, apperrors.BadRequest("baseToken and quoteToken must differ")
	}
	// Settlement pulls each side's asset via transferFrom against a prior
	// approve() (see EscrowAddress's doc comment) - native ETH has no
	// approve/transferFrom concept, so only curated ERC-20 pairs can be
	// traded through this engine. A native-asset market is a natural
	// extension once an escrow *contract* exists (Phase 8) that can
	// receive ETH directly rather than needing an allowance.
	if baseToken == "" || quoteToken == "" {
		return nil, apperrors.BadRequest("native ETH cannot be traded on the market - use a curated ERC-20 pair")
	}
	if _, err := s.tokenDecimals(baseToken); err != nil {
		return nil, err
	}
	if _, err := s.tokenDecimals(quoteToken); err != nil {
		return nil, err
	}

	offer := &models.MarketOffer{
		ID:                uuid.NewString(),
		UserID:            user.ID,
		MakerAddress:      address,
		OfferType:         offerType,
		BaseTokenAddress:  baseToken,
		QuoteTokenAddress: quoteToken,
		PricePerUnit:      price.String(),
		Quantity:          qty.String(),
		RemainingQuantity: qty.String(),
		Status:            models.OfferOpen,
	}
	if err := s.DB.Create(offer).Error; err != nil {
		return nil, apperrors.Internal("failed to save offer")
	}

	s.matchOffer(offer)
	s.DB.First(offer, "id = ?", offer.ID)
	return offer, nil
}

// matchOffer repeatedly finds the best compatible resting offer and
// settles as much as it can, until offer is filled or no more compatible
// offers exist. Plays the role the original's live Stellar DEX order book
// played there - matching and settling trades - since Base has no
// protocol-level equivalent to place a resting offer against.
func (s *Service) matchOffer(offer *models.MarketOffer) {
	for {
		remaining, _ := decimal.NewFromString(offer.RemainingQuantity)
		if !remaining.IsPositive() {
			return
		}

		counter, ok := s.findBestCounterOffer(offer)
		if !ok {
			return
		}

		s.settleMatch(offer, counter)
		if err := s.DB.First(offer, "id = ?", offer.ID).Error; err != nil {
			return
		}
	}
}

// findBestCounterOffer returns the best-priced, oldest-at-that-price OPEN
// or PARTIALLY_FILLED offer on the opposite side of offer's book that
// crosses its price, or ok=false if none exists.
func (s *Service) findBestCounterOffer(offer *models.MarketOffer) (*models.MarketOffer, bool) {
	price, _ := decimal.NewFromString(offer.PricePerUnit)
	var counter models.MarketOffer
	query := s.DB.Where(
		"base_token_address = ? AND quote_token_address = ? AND status IN ?",
		offer.BaseTokenAddress, offer.QuoteTokenAddress, []models.OfferStatus{models.OfferOpen, models.OfferPartiallyFilled},
	).Where("user_id <> ?", offer.UserID)

	// price_per_unit is stored as text; CAST to a numeric type for
	// comparison and ordering - supported by both sqlite and postgres.
	if offer.OfferType == models.OfferBuy {
		query = query.Where("offer_type = ? AND CAST(price_per_unit AS DECIMAL) <= ?", models.OfferSell, price.String())
		query = query.Order("CAST(price_per_unit AS DECIMAL) ASC, created_at ASC")
	} else {
		query = query.Where("offer_type = ? AND CAST(price_per_unit AS DECIMAL) >= ?", models.OfferBuy, price.String())
		query = query.Order("CAST(price_per_unit AS DECIMAL) DESC, created_at ASC")
	}

	if err := query.First(&counter).Error; err != nil {
		return nil, false
	}
	return &counter, true
}

// settleMatch executes one match between offer and counter at counter's
// price (the resting offer - standard price-time-priority convention),
// for min(both sides' remaining quantity), by pulling each side's asset
// via transferFrom against their prior approve() of EscrowAddress().
func (s *Service) settleMatch(offer, counter *models.MarketOffer) {
	var buy, sell *models.MarketOffer
	if offer.OfferType == models.OfferBuy {
		buy, sell = offer, counter
	} else {
		buy, sell = counter, offer
	}

	execPrice, _ := decimal.NewFromString(counter.PricePerUnit)
	buyRemaining, _ := decimal.NewFromString(buy.RemainingQuantity)
	sellRemaining, _ := decimal.NewFromString(sell.RemainingQuantity)
	matchQty := decimal.Min(buyRemaining, sellRemaining)
	if !matchQty.IsPositive() {
		return
	}
	quoteAmount := matchQty.Mul(execPrice)

	baseDecimals, err := s.tokenDecimals(offer.BaseTokenAddress)
	if err != nil {
		return
	}
	quoteDecimals, err := s.tokenDecimals(offer.QuoteTokenAddress)
	if err != nil {
		return
	}

	escrowKey, err := s.deriveEscrowKey()
	if err != nil {
		return
	}

	// Leg 1: seller -> buyer, base asset.
	baseTxHash, err := s.transferFromEscrow(escrowKey, sell.MakerAddress, buy.MakerAddress, offer.BaseTokenAddress, toBaseUnits(matchQty, baseDecimals))
	if err != nil {
		s.cancelForFailedSettlement(sell)
		return
	}
	// Leg 2: buyer -> seller, quote asset.
	quoteTxHash, err := s.transferFromEscrow(escrowKey, buy.MakerAddress, sell.MakerAddress, offer.QuoteTokenAddress, toBaseUnits(quoteAmount, quoteDecimals))
	if err != nil {
		// The base leg already moved - this is a genuine partial-settlement
		// failure (the buyer's approve/balance was insufficient after the
		// seller's leg cleared). Canceling the buyer's remaining offer stops
		// it from being matched again in this state; the seller is not
		// made whole automatically. This asymmetry - two independent
		// transferFrom calls instead of one atomic swap - is a known
		// limitation of settling without an escrow contract; see PLAN.md
		// §4.8's smart-accounts/v2 note for the natural fix (an atomic
		// on-chain order-book contract once Phase 8 exists).
		s.cancelForFailedSettlement(buy)
		return
	}

	s.recordTrade(buy, sell, matchQty, execPrice, offer.BaseTokenAddress, offer.QuoteTokenAddress, baseTxHash, quoteTxHash)
}

func (s *Service) transferFromEscrow(escrowKey *ecdsa.PrivateKey, from, to, tokenAddress string, amount *big.Int) (string, error) {
	data, err := network.EncodeERC20TransferFrom(from, to, amount)
	if err != nil {
		return "", err
	}
	tokenAddr := common.HexToAddress(tokenAddress)
	return s.Blockchain.SignAndSubmitTx(context.Background(), escrowKey, &tokenAddr, big.NewInt(0), data, nil)
}

func (s *Service) cancelForFailedSettlement(offer *models.MarketOffer) {
	s.DB.Model(offer).Update("status", models.OfferCanceled)
}

func (s *Service) recordTrade(buy, sell *models.MarketOffer, quantity, price decimal.Decimal, baseToken, quoteToken, baseTxHash, quoteTxHash string) {
	buyRemaining, _ := decimal.NewFromString(buy.RemainingQuantity)
	sellRemaining, _ := decimal.NewFromString(sell.RemainingQuantity)
	newBuyRemaining := buyRemaining.Sub(quantity)
	newSellRemaining := sellRemaining.Sub(quantity)

	s.DB.Model(buy).Updates(map[string]interface{}{
		"remaining_quantity": newBuyRemaining.String(),
		"status":             statusFor(newBuyRemaining),
	})
	s.DB.Model(sell).Updates(map[string]interface{}{
		"remaining_quantity": newSellRemaining.String(),
		"status":             statusFor(newSellRemaining),
	})

	s.DB.Create(&models.MarketTrade{
		ID:                  newTradeID(),
		BuyOfferID:          buy.ID,
		SellOfferID:         sell.ID,
		BaseTokenAddress:    baseToken,
		QuoteTokenAddress:   quoteToken,
		Price:               price.String(),
		Quantity:            quantity.String(),
		BaseTransferTxHash:  baseTxHash,
		QuoteTransferTxHash: quoteTxHash,
	})
}

func statusFor(remaining decimal.Decimal) models.OfferStatus {
	if remaining.IsZero() {
		return models.OfferFilled
	}
	return models.OfferPartiallyFilled
}
