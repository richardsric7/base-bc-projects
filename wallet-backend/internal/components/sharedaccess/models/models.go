// Package models defines the persisted shapes for shared/multi-party wallet
// access - see PLAN.md §2 (the "native multi-signature" row) and §4.2 for
// the full design. A "group" is a wallet controlled by a threshold of its
// members' approvals rather than any single private key: the group's own
// Base address is derived server-side (see services.New) and never held by
// any one member, so no member can move funds alone, matching the security
// property Stellar's native multi-sig gave the original.
package models

import "time"

// GroupRole is a member's permission level on a shared-access group,
// mirroring the original's WalletPermission values.
type GroupRole string

const (
	RoleInitiator GroupRole = "INITIATOR" // may propose actions
	RoleApprover  GroupRole = "APPROVER"  // may approve/reject proposed actions
	RoleViewOnly  GroupRole = "VIEW_ONLY" // may view balances/history only
	// RoleInitiatorApprover combines both capabilities in a single
	// GroupMember row - needed because GroupMember allows only one row
	// per (group, address) pair, yet a sub-wallet's sole owner (PLAN.md
	// §13.4: "every wallet, the primary included, is a ClosedGroup row
	// from the moment it's created") needs both: they must be able to
	// propose their own actions (INITIATOR) and their own approval must
	// count toward the group's Safe-enforced threshold (APPROVER).
	RoleInitiatorApprover GroupRole = "INITIATOR_APPROVER"
)

// CanInitiate reports whether role may propose an action on a group.
func CanInitiate(role GroupRole) bool {
	return role == RoleInitiator || role == RoleInitiatorApprover
}

// CanApprove reports whether role may approve/reject a proposed action -
// and, equivalently, whether a member holding it becomes one of the
// underlying Safe's on-chain owners (PLAN.md §13.4/§13.6): only a member
// whose signature can actually satisfy the group's threshold needs to be
// named as a Safe owner at all.
func CanApprove(role GroupRole) bool {
	return role == RoleApprover || role == RoleInitiatorApprover
}

// ClosedGroupPurpose distinguishes what a ClosedGroup row is for. Both
// purposes share one table (and one ClosedGroup/GroupMember schema) rather
// than duplicating a near-identical "named group of member addresses"
// concept - see PLAN.md §4.9's closed-group/private-offering write-up for
// why tokenization's private-offering gating reuses this table instead of
// its own.
type ClosedGroupPurpose string

const (
	// PurposeWalletAccess is this package's original use: a group that
	// jointly controls a shared Base wallet (Address/Threshold populated,
	// PendingAction/PendingActionApproval rows apply).
	PurposeWalletAccess ClosedGroupPurpose = "WALLET_ACCESS"
	// PurposePrivateOffering is tokenization's use: a group whose
	// membership list alone gates who may see/subscribe to a private
	// tokenized-asset offering - no wallet, no threshold, no on-chain
	// address of its own, so Address is nil and Threshold is unused for
	// these rows.
	PurposePrivateOffering ClosedGroupPurpose = "PRIVATE_OFFERING"
)

// ClosedGroup is a named group of member addresses, used two ways
// (Purpose): a shared wallet whose controlling key is deterministically
// derived (cryptoutil.DeriveKey) from the group's ID and never exposed to
// any member, where Threshold is how many distinct APPROVER approvals a
// PendingAction needs before the server executes it; or, for tokenization,
// a plain allow-list of addresses permitted to subscribe to a private
// offering. Address is a pointer because only PurposeWalletAccess rows
// have one - a nullable column lets multiple PurposePrivateOffering rows
// coexist under one uniqueIndex (SQL treats NULLs as distinct from each
// other, unlike empty strings).
type ClosedGroup struct {
	ID        uint               `gorm:"primaryKey" json:"id"`
	Name      string             `gorm:"size:128;not null" json:"name"`
	Purpose   ClosedGroupPurpose `gorm:"size:20;not null;default:WALLET_ACCESS" json:"purpose"`
	Address   *string            `gorm:"uniqueIndex;size:42" json:"address,omitempty"`
	Threshold int                `json:"threshold,omitempty"`
	Disabled  bool               `gorm:"default:false" json:"disabled"`
	CreatedAt time.Time          `json:"createdAt"`
}

// GroupMember is one member's role on a group.
type GroupMember struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	GroupID       uint      `gorm:"uniqueIndex:idx_group_member;not null" json:"groupId"`
	MemberAddress string    `gorm:"uniqueIndex:idx_group_member;size:42;not null" json:"memberAddress"`
	Role          GroupRole `gorm:"size:16;not null" json:"role"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ActionStatus is a PendingAction's lifecycle state.
type ActionStatus string

const (
	ActionPending ActionStatus = "PENDING"
	// ActionSubmitted is a transient state: the approval threshold was
	// met and a relayer has broadcast the real Safe execTransaction call,
	// but its confirmation hasn't been observed yet (PLAN.md §13.10
	// Phase 4). It blocks new approvals the same way PENDING's own
	// terminal-state check does, preventing a second concurrent
	// submission of the same action. A failed or unconfirmed submission
	// reverts to ActionPending so the next approval call retries
	// execution (see services.ApproveAction's already-approved branch).
	ActionSubmitted ActionStatus = "SUBMITTED"
	ActionExecuted  ActionStatus = "EXECUTED"
	ActionRejected  ActionStatus = "REJECTED"
)

// ActionKind distinguishes what a proposed action does, purely for display
// - the actual on-chain effect is fully described by To/TokenAddress/Value/Data.
type ActionKind string

const (
	ActionPayment      ActionKind = "payment"
	ActionSwap         ActionKind = "swap"
	ActionContractCall ActionKind = "contract_call"
	// ActionAddMember, ActionRemoveMember and ActionChangeThreshold
	// (PLAN.md §13.10 Phase 5) are the group-management equivalents of
	// the original's ModifySharedWalletAccess - gated through the exact
	// same propose/approve/execute pipeline as any other action. Adding
	// or removing a member who holds (or would hold) an approve-capable
	// role maps to a real Safe self-call (addOwnerWithThreshold/
	// removeOwner - see TargetMemberAddress/TargetRole/NewThreshold);
	// changing a VIEW_ONLY/plain-INITIATOR member's membership, or the
	// threshold-only case, never touches the Safe's own owner set - see
	// services.createPendingAction's To=="" branch.
	ActionAddMember       ActionKind = "add_member"
	ActionRemoveMember    ActionKind = "remove_member"
	ActionChangeThreshold ActionKind = "change_threshold"
	// ActionDisableGroup (PLAN.md §13.10 Phase 5) is the equivalent of
	// the original's DELETE /v1/shared-access/users/account - a pure
	// application-level flag (ClosedGroup.Disabled), never a Safe call:
	// disabling only stops this application from proposing further
	// actions against the group, since the underlying Safe itself has no
	// "disabled" concept of its own.
	ActionDisableGroup ActionKind = "disable_group"
)

// PendingAction is one proposed group action awaiting approval - the
// equivalent of the original's PendingAuth, generalized to any transaction
// shape rather than one row per component (payment/swap/etc.), see
// PLAN.md §4.2.
type PendingAction struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	GroupID           uint       `gorm:"index;not null" json:"groupId"`
	ProposerAddress   string     `gorm:"size:42;not null" json:"proposerAddress"`
	Kind              ActionKind `gorm:"size:20;not null" json:"kind"`
	Description       string     `gorm:"size:512" json:"description"`
	To                string     `gorm:"size:42;not null" json:"to"`
	TokenAddress      string     `gorm:"size:42" json:"tokenAddress"` // empty = native ETH
	Value             string     `gorm:"size:80;not null" json:"value"`
	Data              string     `gorm:"type:text" json:"data"` // hex calldata, "0x" if none
	RequiredApprovals int        `gorm:"not null" json:"requiredApprovals"`
	// SafeNonce is the group Safe's on-chain nonce this action's SafeTx
	// was built against, read once (services.createPendingAction) at
	// proposal time and fixed from then on - every approver must sign the
	// same SafeTxHash, which depends on it. See safe.EncodeNonceCalldata's
	// doc comment for the concurrency race this naive read defers to
	// PLAN.md §13.10 Phase 6.
	SafeNonce string       `gorm:"size:80" json:"safeNonce"`
	Status    ActionStatus `gorm:"size:16;not null;default:PENDING" json:"status"`
	TxHash    string       `gorm:"size:66" json:"txHash"`
	// RelayerAddress is the pool relayer (internal/relayer) currently
	// submitting - or that most recently submitted - this action's
	// execTransaction call. Only meaningful while Status is SUBMITTED;
	// used by services.ReconcileRelayers to re-mark the right relayer
	// in-use after a restart (PLAN.md §13.12).
	RelayerAddress string `gorm:"size:42" json:"relayerAddress,omitempty"`
	// TargetMemberAddress/TargetRole/NewThreshold are populated only for
	// the group-management kinds above (ActionAddMember/RemoveMember/
	// ChangeThreshold/DisableGroup) - see their doc comments. Unused
	// (zero-valued) for ordinary payment/swap/contract_call actions.
	TargetMemberAddress string    `gorm:"size:42" json:"targetMemberAddress,omitempty"`
	TargetRole          GroupRole `gorm:"size:20" json:"targetRole,omitempty"`
	NewThreshold        int       `json:"newThreshold,omitempty"`
	// Domain and RelatedRecordID (PLAN.md §13.8/§13.10 Phase 7) let a
	// business component outside this package (crypto withdrawals,
	// tokenization, market-making, ...) attach its own domain record to a
	// PendingAction it proposes via ProposePayment/ProposeContractCall,
	// and be notified once this action reaches a terminal state via a
	// services.DomainHook registered under the same Domain string - see
	// services.RegisterDomainHook. Empty for an ordinary group-proposed
	// action; this package itself never reads or interprets either
	// field beyond passing them back to the right hook.
	Domain          string    `gorm:"size:40" json:"domain,omitempty"`
	RelatedRecordID string    `gorm:"size:80" json:"relatedRecordId,omitempty"`
	RejectionReason string    `gorm:"size:512" json:"rejectionReason"`
	CreatedAt       time.Time `json:"createdAt"`
}

// PendingActionApproval is one member's off-chain co-signature of a
// PendingAction's real digest (the on-chain SafeTxHash, or its nested
// EIP-1271 wrapping) - the equivalent of the original's
// PendingTransactionSignature. See services.digestToSign for exactly what
// gets signed.
type PendingActionApproval struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	PendingActionID uint      `gorm:"uniqueIndex:idx_action_approver;not null" json:"pendingActionId"`
	MemberAddress   string    `gorm:"uniqueIndex:idx_action_approver;size:42;not null" json:"memberAddress"`
	Signature       string    `gorm:"size:132;not null" json:"signature"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&ClosedGroup{},
	&GroupMember{},
	&PendingAction{},
	&PendingActionApproval{},
}
