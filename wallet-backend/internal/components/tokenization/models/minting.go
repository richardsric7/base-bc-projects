package models

import "time"

// MintApprovalStatus is a mint request's lifecycle.
type MintApprovalStatus string

const (
	MintApprovalPending  MintApprovalStatus = "PENDING"
	MintApprovalExecuted MintApprovalStatus = "EXECUTED"
)

// MintApproval is one asset's pending mint request, awaiting signoff from
// the asset's own MintingApprovers CSV before the server deploys/mints -
// upstream's per-asset ≥4-approver multisig gate, kept as its own
// mechanism (not shared-access's PendingAction/PendingActionApproval)
// because this is staff sign-off on an admin action, not a group wallet's
// own transaction - the same separation upstream itself drew between
// TokenizationMintingApprover/Initiator and the general wallet-multisig
// approvals system. See PLAN.md §4.9.
type MintApproval struct {
	ID                uint               `gorm:"primaryKey" json:"id"`
	TokenizedAssetID  uint               `gorm:"uniqueIndex;not null" json:"tokenizedAssetId"`
	RequiredApprovals int                `gorm:"not null" json:"requiredApprovals"`
	Status            MintApprovalStatus `gorm:"size:16;not null;default:PENDING" json:"status"`

	// Filled in once Status flips to EXECUTED.
	IssuerContractAddress *string `gorm:"size:42" json:"issuerContractAddress"`
	SaleContractAddress   *string `gorm:"size:42" json:"saleContractAddress"`
	DistributionAddress   *string `gorm:"size:42" json:"distributionAddress"`
	MintTxHash            string  `gorm:"size:66" json:"mintTxHash"`

	CreatedAt time.Time `json:"createdAt"`
}

// MintApprovalSignoff is one approver's signed confirmation - an EIP-191
// personal_sign over a canonical description of the mint request, the
// same signature-based approval primitive shared-access's
// PendingActionApproval uses.
type MintApprovalSignoff struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	MintApprovalID  uint      `gorm:"uniqueIndex:idx_mint_approval_signer;not null" json:"mintApprovalId"`
	ApproverAddress string    `gorm:"uniqueIndex:idx_mint_approval_signer;size:42;not null" json:"approverAddress"`
	Signature       string    `gorm:"size:132;not null" json:"signature"`
	CreatedAt       time.Time `json:"createdAt"`
}
