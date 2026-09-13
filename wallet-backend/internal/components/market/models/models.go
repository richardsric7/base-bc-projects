// Package models defines the persisted shapes owned by the market
// component: an off-chain, server-matched limit-order book, standing in
// for Stellar's protocol-level DEX the original placed resting offers on
// directly - Base has no equivalent, so this had to be a real design
// choice rather than a mechanical port. See PLAN.md §4.8 for the full
// design, including why settlement uses transferFrom against a prior
// approve() (the same off-chain-order/on-chain-settlement pattern the 0x
// Protocol popularized) rather than the server ever holding user funds.
package models

import "time"

// OfferType is which side of the book an offer rests on.
type OfferType string

const (
	OfferBuy  OfferType = "BUY"
	OfferSell OfferType = "SELL"
)

// OfferStatus is an offer's lifecycle state.
type OfferStatus string

const (
	OfferOpen            OfferStatus = "OPEN"
	OfferPartiallyFilled OfferStatus = "PARTIALLY_FILLED"
	OfferFilled          OfferStatus = "FILLED"
	OfferCanceled        OfferStatus = "CANCELED"
)

// MarketOffer is one resting limit order. BaseTokenAddress/QuoteTokenAddress
// are empty for native ETH, matching this port's existing "empty = native"
// convention (see assets/payments). PricePerUnit is quote-per-1-base, a
// decimal string (never a float, to avoid floating-point drift across
// repeated partial fills). Ported from the original's MarketOffer, with
// FeeChargedOnAsset/FeeValue/NetQuantity dropped - the original's own
// market-making fee was already disabled upstream (hardcoded to zero, per
// its own code comment "fees r now removed"), so there was no working fee
// behavior to preserve.
type MarketOffer struct {
	ID                string      `gorm:"primaryKey;size:64" json:"id"`
	UserID            uint        `gorm:"index;not null" json:"userId"`
	MakerAddress      string      `gorm:"size:42;not null" json:"makerAddress"`
	OfferType         OfferType   `gorm:"size:8;not null" json:"offerType"`
	BaseTokenAddress  string      `gorm:"size:42;index:idx_market_pair" json:"baseTokenAddress"`
	QuoteTokenAddress string      `gorm:"size:42;index:idx_market_pair" json:"quoteTokenAddress"`
	PricePerUnit      string      `gorm:"size:64;not null" json:"pricePerUnit"`
	Quantity          string      `gorm:"size:64;not null" json:"quantity"`
	RemainingQuantity string      `gorm:"size:64;not null" json:"remainingQuantity"`
	Status            OfferStatus `gorm:"size:20;not null;default:OPEN" json:"status"`
	CreatedAt         time.Time   `json:"createdAt"`
}

// MarketTrade is a permanent audit record of one executed match between
// two offers - always settled at the resting (older) offer's price, the
// standard price-time-priority convention.
type MarketTrade struct {
	ID                  string    `gorm:"primaryKey;size:64" json:"id"`
	BuyOfferID          string    `gorm:"index;size:64;not null" json:"buyOfferId"`
	SellOfferID         string    `gorm:"index;size:64;not null" json:"sellOfferId"`
	BaseTokenAddress    string    `gorm:"size:42" json:"baseTokenAddress"`
	QuoteTokenAddress   string    `gorm:"size:42" json:"quoteTokenAddress"`
	Price               string    `gorm:"size:64;not null" json:"price"`
	Quantity            string    `gorm:"size:64;not null" json:"quantity"`
	BaseTransferTxHash  string    `gorm:"size:66" json:"baseTransferTxHash"`
	QuoteTransferTxHash string    `gorm:"size:66" json:"quoteTransferTxHash"`
	CreatedAt           time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&MarketOffer{},
	&MarketTrade{},
}
