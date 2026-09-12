package models

import "time"

// This file ports the dormant proceed/dividend payout-schedule engine's
// schema only - no service function or worker ever populates or reads
// these tables. Upstream's own equivalent (payouts.go) is a 160-line file
// whose real logic sits in a dead `func main()` inside `package users`
// (hardcoded placeholder asset/issuer constants, never invoked from
// anywhere else in the codebase), and TOKENIZATION_PLAN.md itself notes
// the production payout engine was never tracked down. Per PLAN.md §9's
// resolved policy this is ported-without-wiring, not skipped and not
// finished - carrying forward the exact same "exists in schema, does
// nothing" state it has upstream, documented rather than silently kept.

// ProceedPayout is a proceeds-distribution batch header for one asset's
// payout cycle.
type ProceedPayout struct {
	ID                          uint      `gorm:"primaryKey" json:"id"`
	CreatedAt                   time.Time `json:"createdAt"`
	TokenizedAssetID            uint      `gorm:"index;not null" json:"tokenizedAssetId"`
	Batch                       string    `gorm:"uniqueIndex;size:100;not null" json:"batch"` // asset code + cycle + month + year
	DepositedProceedAmount      string    `json:"depositedProceedAmount"`
	PlatformFee                 string    `json:"platformFee"`
	ProceedPayoutAmount         string    `json:"proceedPayoutAmount"`
	AmountPerTokenizedAssetHeld string    `json:"amountPerTokenizedAssetHeld"`
	PaymentScheduleReady        bool      `gorm:"default:false" json:"paymentScheduleReady"`
	PayoutCompleted             bool      `gorm:"default:false" json:"payoutCompleted"`
}

// TokenizedAssetPayoutSchedule is one beneficiary's line item within a
// ProceedPayout batch.
type TokenizedAssetPayoutSchedule struct {
	ID                             uint      `gorm:"primaryKey" json:"id"`
	CreatedAt                      time.Time `json:"createdAt"`
	TokenizedAssetID               uint      `gorm:"index;not null" json:"tokenizedAssetId"`
	Batch                          string    `gorm:"index;size:100;not null" json:"batch"`
	PayoutAssetSymbol              string    `gorm:"size:12;not null" json:"payoutAssetSymbol"`
	BeneficiaryAddress             string    `gorm:"size:42;not null" json:"beneficiaryAddress"`
	ConfirmedTokenizedAssetBalance string    `json:"confirmedTokenizedAssetBalance"`
	AmountToReceive                string    `json:"amountToReceive"`
	CannotReceiveAsset             bool      `gorm:"default:false" json:"cannotReceiveAsset"`
}

// TokenizedAssetPayoutEngineTask is one payment-execution task derived
// from a TokenizedAssetPayoutSchedule row.
type TokenizedAssetPayoutEngineTask struct {
	ID                             uint      `gorm:"primaryKey" json:"id"`
	CreatedAt                      time.Time `json:"createdAt"`
	TokenizedAssetPayoutScheduleID uint      `gorm:"uniqueIndex;not null" json:"tokenizedAssetPayoutScheduleId"`
	MemoFromBatch                  string    `gorm:"index;size:100;not null" json:"batch"`
	PayoutAssetSymbol              string    `gorm:"size:12;not null" json:"payoutAssetSymbol"`
	BeneficiaryAddress             string    `gorm:"size:42;not null" json:"beneficiaryAddress"`
	AmountToReceive                string    `json:"amountToReceive"`
	Paid                           bool      `gorm:"default:false" json:"paid"`
}
