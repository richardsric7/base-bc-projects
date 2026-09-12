// Package models defines the persisted shapes owned by the crypto
// component: OneLiquidity-backed external-crypto deposit and withdrawal,
// same chain-agnostic vendor-integration shape as kyc/fiat/stablerail -
// see PLAN.md §4.7 for the full design, including why deposit crediting
// here is a treasury transfer rather than the original's Stellar mint.
package models

import "time"

// CryptoDepositAddress is one OneLiquidity-issued deposit address for a
// user, currency and network - a user typically has one per
// (currency, network) pair once they've requested a deposit address for
// it. Ported from the original's CryptoWalletDepositAddress.
type CryptoDepositAddress struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	UserID         uint      `gorm:"uniqueIndex:idx_deposit_address;not null" json:"userId"`
	Currency       string    `gorm:"uniqueIndex:idx_deposit_address;size:16;not null" json:"currency"`
	Network        string    `gorm:"uniqueIndex:idx_deposit_address;size:32;not null" json:"network"`
	DepositAddress string    `gorm:"size:128;not null" json:"depositAddress"`
	CreatedAt      time.Time `json:"createdAt"`
}

// CryptoDeposit is this backend's own record of one external deposit
// OneLiquidity reported, and whether it has been credited to the user's
// Base balance yet. Consolidates the original's two-table split
// (CryptoDeposit + CallbackDepositItem, an audit record and a
// minting-queue row for the same event) into one, since this port credits
// deposits synchronously in the same poll that discovers them rather than
// queueing a separate minting step - see services.go.
type CryptoDeposit struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"index;not null" json:"userId"`
	DepositID    string    `gorm:"uniqueIndex;size:64;not null" json:"depositId"` // OneLiquidity's own deposit ID
	TxID         string    `gorm:"size:128" json:"txId"`
	Currency     string    `gorm:"size:16;not null" json:"currency"`
	Amount       string    `gorm:"size:64;not null" json:"amount"` // decimal string, base currency units (not wei)
	FromAddress  string    `gorm:"size:128" json:"fromAddress"`
	ToAddress    string    `gorm:"size:128" json:"toAddress"`
	Credited     bool      `gorm:"default:false" json:"credited"`
	CreditTxHash string    `gorm:"size:66" json:"creditTxHash"`
	CreatedAt    time.Time `json:"createdAt"`
}

// WithdrawalNetwork is one currency/network's withdrawal limits and fee,
// cached from OneLiquidity (see services.GetWithdrawalNetworks). Ported
// from the original's WithdrawalNetwork.
type WithdrawalNetwork struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Currency    string    `gorm:"uniqueIndex:idx_withdrawal_network;size:16;not null" json:"currency"`
	Network     string    `gorm:"uniqueIndex:idx_withdrawal_network;size:32;not null" json:"network"`
	WithdrawMin string    `gorm:"size:64" json:"withdrawMin"`
	WithdrawMax string    `gorm:"size:64" json:"withdrawMax"`
	WithdrawFee string    `gorm:"size:64" json:"withdrawFee"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// WithdrawalRequest is one user-initiated external crypto withdrawal.
// TreasuryTxHash is the on-chain transaction (submitted by this backend
// from the caller's own signed transfer, exactly like fiat's generic
// invoice-settlement path) that moved the withdrawn amount from the
// user's Base address to the treasury address OneLiquidity's external
// payout is funded against - see services.go for why a Base withdrawal
// needs this leg where the original's Stellar version didn't build one
// itself (a client there submitted its own debit directly).
//
// This port only implements single-owner withdrawal (Status starts
// PENDING and is submitted immediately - no approval gate). The
// original's multi-party shared-access withdrawal path (approve-then-
// submit for a group wallet) is a **documented gap, not a silent drop**:
// reconciling it with sharedaccess.PendingAction's executor - hardcoded
// today to submit an on-chain transaction, not call an external vendor
// API - is a real design decision, not a mechanical port, and a
// shared-access group can still withdraw once that generalization is
// made. See PLAN.md §4.7.
type WithdrawalRequest struct {
	ID                       string    `gorm:"primaryKey;size:64" json:"id"` // a uuid, also used as OneLiquidity's idempotency reference
	UserID                   uint      `gorm:"index;not null" json:"userId"`
	Currency                 string    `gorm:"size:16;not null" json:"currency"`
	Network                  string    `gorm:"size:32;not null" json:"network"`
	ToAddress                string    `gorm:"size:128;not null" json:"toAddress"`
	AmountSubmitted          float64   `json:"amountSubmitted"`
	ServiceFeeAmount         float64   `json:"serviceFeeAmount"`
	NetworkFeeAmount         float64   `json:"networkFeeAmount"`
	AmountToWithdraw         float64   `json:"amountToWithdraw"`
	TreasuryTxHash           string    `gorm:"size:66" json:"treasuryTxHash"`
	OneLiquidityWithdrawalID string    `gorm:"size:64" json:"oneLiquidityWithdrawalId"`
	Status                   string    `gorm:"size:32;not null;default:PENDING" json:"status"`
	CreatedAt                time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&CryptoDepositAddress{},
	&CryptoDeposit{},
	&WithdrawalNetwork{},
	&WithdrawalRequest{},
}
