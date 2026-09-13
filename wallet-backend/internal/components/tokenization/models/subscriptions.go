package models

import "time"

// TokenizedAssetSubscription is a completed primary-sale purchase record -
// created after a crypto Sale.buy() confirms, or after a fiat invoice
// settles. See PLAN.md §4.9's primary-sale write-ups.
type TokenizedAssetSubscription struct {
	ID                 string    `gorm:"primaryKey;size:64" json:"id"` // shared with the fiat invoice ID on the fiat path
	CreatedAt          time.Time `json:"createdAt"`
	TokenizedAssetID   uint      `gorm:"index;not null" json:"tokenizedAssetId"`
	SubscriberUserID   uint      `gorm:"index;not null" json:"subscriberUserId"`
	SubscriberAddress  string    `gorm:"size:42;not null" json:"subscriberAddress"`
	Quantity           string    `gorm:"not null" json:"quantity"`      // base units of the tokenized asset
	PaymentAmount      string    `gorm:"not null" json:"paymentAmount"` // base units of the quote currency
	PaymentAssetSymbol string    `gorm:"size:12;not null" json:"paymentAssetSymbol"`
	TxHash             string    `gorm:"size:66" json:"txHash"`
	Channel            string    `gorm:"size:8;not null" json:"channel"` // CRYPTO or FIAT
}

// ExpressionOfInterest is a pre-launch waitlist entry, notified once the
// asset's primary sale activates.
type ExpressionOfInterest struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	CreatedAt        time.Time `json:"createdAt"`
	TokenizedAssetID uint      `gorm:"uniqueIndex:idx_interest_unique;not null" json:"tokenizedAssetId"`
	UserID           uint      `gorm:"uniqueIndex:idx_interest_unique;not null" json:"userId"`
	Amount           string    `json:"amount"` // indicative quote-currency amount the subscriber intends to spend
	Notified         bool      `gorm:"default:false" json:"notified"`
}

// TokenizedAssetEarlyExit is a pre-maturity redemption record: the holder
// burns their own balance directly (TokenizedAsset.sol is ERC20Burnable -
// no server-signed transfer needed, unlike upstream's payment-to-
// distribution-wallet hop), and this row records the NAV-based penalty
// payout for off-chain/manual settlement, exactly as upstream never
// automated this payout on-chain either. See PLAN.md §4.9.
type TokenizedAssetEarlyExit struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	CreatedAt        time.Time `json:"createdAt"`
	TokenizedAssetID uint      `gorm:"index;not null" json:"tokenizedAssetId"`
	HolderUserID     uint      `gorm:"index;not null" json:"holderUserId"`
	HolderAddress    string    `gorm:"size:42;not null" json:"holderAddress"`

	BankID         uint   `json:"bankId"`
	AccountNumber  string `json:"accountNumber"`
	AccountName    string `json:"accountName"`
	PayoutCurrency string `json:"payoutCurrency"` // a CuratedToken symbol, = the asset's AssetQuoteCurrency
	BurnTxHash     string `gorm:"size:66" json:"burnTxHash"`

	TokenQuantityExited   string `json:"tokenQuantityExited"`
	NAVPerTokenAtExit     string `json:"navPerTokenAtExit"`
	PenaltyPercentApplied string `json:"penaltyPercentApplied"`
	PayoutPricePerToken   string `json:"payoutPricePerToken"`
	EstimatedPayoutAmount string `json:"estimatedPayoutAmount"`
}
