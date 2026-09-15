// Package models defines the persisted shapes owned by the users component.
package models

import "time"

// User is an account holder. The wallet is non-custodial: SignerAddress is
// the EVM EOA recovered from a verified per-request signature
// (middleware.SignatureAuth, PLAN.md §12) - the server never sees, let
// alone stores, the matching private key. Address is that signer's
// primary wallet: a Gnosis Safe smart-contract account (PLAN.md §13.2/
// §13.3) whose sole owner is SignerAddress, computed once at registration
// via safe.ComputeProxyAddress and never changed afterward. Splitting the
// two is what makes wallet recovery (PLAN.md §15.6) possible at all:
// losing SignerAddress's key only ever requires swapping Address's Safe
// owner to a new key - the permanent identity every other wallet's owner
// list, every counterparty, and every stored balance already references
// never has to move.
type User struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Username string `gorm:"uniqueIndex;size:32;not null" json:"username"`
	Email    string `gorm:"uniqueIndex;size:255;not null" json:"email"`
	Address  string `gorm:"uniqueIndex;size:42;not null" json:"address"`
	// SignerAddress is the EOA currently authorized to operate the
	// primary wallet - the key middleware.SignatureAuth accepts as the
	// signer for a request naming Address as X-Wallet-Address (see that
	// middleware's usersModels lookup). Unique for the same reason
	// Address is: two users can never share a controlling key any more
	// than they can share an identity.
	SignerAddress string `gorm:"uniqueIndex;size:42;not null" json:"signerAddress"`
	// PrimaryWalletDeployed records whether Address has actually been
	// deployed on-chain yet (a createProxyWithNonce call has succeeded) -
	// always false immediately after registration, since registration
	// deliberately never requires on-chain activation (PLAN.md §13.11).
	// Required before this wallet can execute anything at all: fund a
	// sub-wallet, enable shared access, or be named as another wallet's
	// owner - see services.DeployPrimaryWallet.
	PrimaryWalletDeployed bool   `gorm:"default:false" json:"primaryWalletDeployed"`
	KYCStatus             string `gorm:"size:32;default:pending" json:"kycStatus"`
	// KYCVerifiedLevel is the highest identity-verification level this user
	// has completed with either vendor (see internal/components/kyc) - 0
	// means unverified. Ported from the original's User.KYCVerified; other
	// phases (fiat activation limits, tokenization purchase limits) gate on
	// this field, exactly as they did upstream.
	KYCVerifiedLevel       int  `gorm:"default:0" json:"kycVerifiedLevel"`
	AccountRecoveryEnabled bool `gorm:"default:false" json:"accountRecoveryEnabled"`
	// WalletRecoveryEnabled gates Branch B - true wallet-signer recovery
	// via a Safe owner swap (PLAN.md §15) - and is deliberately
	// independent of AccountRecoveryEnabled (§15.2a): a user may enable
	// neither, either, or both. Requires PrimaryWalletDeployed, since
	// enabling it means adding the recovery service as a second owner of
	// an already-deployed Safe.
	WalletRecoveryEnabled bool `gorm:"default:false" json:"walletRecoveryEnabled"`
	// Activated records whether this user has completed the one-time paid
	// activation flow (see internal/components/fiat) that dispenses
	// starter gas and a reward token - checked so the payout can never be
	// claimed twice, even if the provider's webhook is delivered more than
	// once for the same successful charge.
	Activated bool      `gorm:"default:false" json:"activated"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// CreatedByServiceLinkID marks an account onboarded by a servicelinks
	// partner (see internal/components/servicelinks) rather than
	// self-registered - nil for an organic registration. Every servicelinks
	// route that acts on a specific user's wallet (payments, KYC overrides,
	// tokenization actions) requires this to equal the calling service
	// link's own ID, checked centrally rather than per-route (PLAN.md
	// §4.11 findings 5 and 6 - the original had this scoping only on some
	// routes, and its KYC-override check treated a nil value here as "skip
	// the check" instead of "deny").
	CreatedByServiceLinkID *uint `gorm:"index" json:"createdByServiceLinkId,omitempty"`
	// RegistrationCountryCode/RegistrationHighRisk are a best-effort
	// signal captured at registration time via internal/geoip resolving
	// the caller's IP, and reference.CountryConfig.HighRisk for that
	// country (see internal/components/reference, PLAN.md §4.13). Empty/
	// false whenever no geo-IP provider is configured or the lookup
	// fails - registration never blocks on this, it's a review flag, not
	// a gate.
	RegistrationCountryCode string `gorm:"size:2" json:"registrationCountryCode,omitempty"`
	RegistrationHighRisk    bool   `gorm:"default:false" json:"registrationHighRisk,omitempty"`
}

// UserWallet is this user's wallet directory: one row per wallet address
// they own, primary wallet included - mirroring the original's own
// UserWallet table (trovo-wallet-monorepo/backend's
// internal/components/users/models/user.go), where "is this the primary
// wallet" is likewise nothing more than a flag (there PrimaryWallet int,
// here IsPrimary bool) on an otherwise ordinary row, not a structurally
// different record. Every wallet a user can act through - their primary
// Safe (registration creates this row, see services.Register) and every
// sharedaccess group Safe they've created (services.RegisterWalletForAddress,
// called from sharedaccess.CreateGroup) - gets exactly one entry here,
// owned by whoever created it.
//
// Tag/Description/Alias port the original's identically-named fields
// verbatim for display and lookup purposes: Alias is a unique, friendly
// handle a payment can name directly (ResolveRecipient), following the
// original's own convention exactly - a primary wallet's alias is its
// owner's username, an additional wallet's is "<ownerUsername>_<tag>"
// (see the original's own "primaryUsername_tag for sub wallets" comment).
//
// LinkedWalletAddress ports the original's LinkedWalletPublicKey field
// (also a wallet - in the original, always another UserWallet row,
// itself potentially a multisig/shared-access wallet, used to associate
// a token-issuing wallet with its distribution wallet). Carried here as
// schema-only display/reference metadata: the original's deeper
// LinkedWalletMustSign co-signing behavior during asset issuance has no
// equivalent hook in this port's tokenization component (which mints via
// direct contract calls, not a wallet-to-wallet signed transfer), so it
// is not reproduced - ported without wiring, not skipped, per this
// project's established policy for a schema/behavior split (see
// tokenization/models/payout.go's identical treatment of the dormant
// dividend engine).
//
// WalletType ports the original's identically-named field verbatim (see
// its own type doc below) - preserved because the person driving this
// port explicitly requires it, correcting an earlier revision of this
// model that reasoned it was superseded by sharedaccess.ClosedGroup/
// GroupMember/PendingAction (PLAN.md §13) and dropped it. It is not: the
// original's WalletType classifies what KIND of wallet this is
// (ordinary, asset-issuing, market-making, bulk-payment) - a business
// fact about the wallet itself - whereas ClosedGroup/GroupMember answer
// the unrelated question of who controls it and how. The original's
// deeper per-type Stellar mechanics (asset-issuing's AuthRequired/
// Clawback/Revocable trustline flags; market-making/bulk-payment's
// server-derived custodial signer at asymmetric weight) have no direct
// Base/Safe equivalent and are not reproduced here - schema-only where
// the underlying chain mechanic doesn't port, same treatment as
// LinkedWalletAddress above - but the classification itself, and
// everything about it that IS meaningful on Base (which distribution
// wallet a tokenized asset's issuance is linked to via
// LinkedWalletAddress; which wallets tokenization/market-making treat as
// theirs), is preserved.
//
// SharedAccessEnabled/NumberOfApprovalsNeeded/Permissions remain
// deliberately NOT ported: those specifically describe shared-access
// control, which sharedaccess.ClosedGroup/GroupMember/PendingAction
// already own for this port, in a real Safe-backed form the original's
// server-derived-key design never had. Duplicating them here would
// create two disagreeing sources of truth for that one fact;
// WalletSummary.IsOwner/IsShared (§20) is this port's own answer to the
// same "who controls this wallet" question.
type UserWallet struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	UserID      uint       `gorm:"index;not null" json:"userId"`
	Address     string     `gorm:"uniqueIndex;size:42;not null" json:"address"`
	Tag         string     `gorm:"size:64" json:"tag"`
	Description string     `gorm:"size:255" json:"description"`
	WalletType  WalletType `gorm:"default:0" json:"walletType"`
	// Alias is unique across every wallet in the system (not just this
	// user's own) - exactly like the original's own unique index - since
	// it has to be, being the very thing a payment or lookup names
	// directly.
	Alias string `gorm:"uniqueIndex;size:70" json:"alias"`
	// LinkedWalletAddress is another wallet's address, purely a display/
	// reference association - see doc comment above.
	LinkedWalletAddress *string   `gorm:"size:42" json:"linkedWalletAddress,omitempty"`
	IsPrimary           bool      `gorm:"default:false" json:"isPrimary"`
	CreatedAt           time.Time `json:"createdAt"`
}

// WalletType mirrors the original's bare-int UserWallet.WalletType values
// exactly (same four members, same integer values, so any migrated data
// or admin tooling that already speaks in these numbers lines up
// unchanged) but names them, rather than leaving them as the original's
// unexplained magic numbers.
type WalletType int

const (
	// WalletTypeNormal is an ordinary wallet: the primary wallet, or any
	// additional/shared-access wallet with no special asset-issuing,
	// market-making, or bulk-payment role.
	WalletTypeNormal WalletType = 0
	// WalletTypeAssetIssuing is a tokenized-asset issuer's wallet -
	// LinkedWalletAddress names its distribution wallet (see that field's
	// doc comment above).
	WalletTypeAssetIssuing WalletType = 1
	// WalletTypeMarketMaking is a wallet the market-making component
	// (internal/components/market) treats as its own.
	WalletTypeMarketMaking WalletType = 2
	// WalletTypeBulkPayment is a wallet used for bulk/batch payment runs.
	WalletTypeBulkPayment WalletType = 3
)

// SecurityQuestion is one entry in the fixed catalog users pick from when
// setting up account-recovery questions.
type SecurityQuestion struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Question string `gorm:"size:255;not null" json:"question"`
}

// UserSecurityAnswer stores a hashed answer to one of the user's chosen
// security questions, used as one recovery factor alongside email OTP.
type UserSecurityAnswer struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	UserID             uint   `gorm:"index;not null" json:"userId"`
	SecurityQuestionID uint   `gorm:"not null" json:"securityQuestionId"`
	AnswerHash         string `gorm:"size:255;not null" json:"-"`
}

// AccountRecoveryEmailVerification stores a one-time recovery OTP sent to a
// user's registered email. Recovery is deliberately public/unauthenticated
// (see services/recovery.go) since its entire purpose is helping someone
// who can no longer sign in - this record plus a correct set of security
// answers are the two factors that stand in for a signature.
type AccountRecoveryEmailVerification struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"userId"`
	Code      string    `gorm:"size:16;not null" json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// AccountRecoveryLog is a permanent audit record of every completed
// recovery. AuthoritySignature is an EIP-191 signature over a description
// of this exact recovery, produced by a recovery-authority key derived via
// cryptoutil.DeriveKey (see services/recovery.go) - a tamper-evident
// attestation that this specific recovery was authorized by the server,
// standing in for the on-chain co-signature the original's native
// Stellar multi-sig recovery flow used (there's nothing to co-sign
// on-chain here, since re-pointing which address controls a username is a
// purely application-level change, not a chain operation - see PLAN.md §4.3).
type AccountRecoveryLog struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Username           string    `gorm:"index;size:32;not null" json:"username"`
	OldAddress         string    `gorm:"size:42;not null" json:"oldAddress"`
	NewAddress         string    `gorm:"size:42;not null" json:"newAddress"`
	AuthoritySignature string    `gorm:"size:132;not null" json:"authoritySignature"`
	CreatedAt          time.Time `json:"createdAt"`
}

// ReservedName is a username no one may register (staff handles, brand
// names, obviously-impersonation-prone strings). Checked at registration.
type ReservedName struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;size:32;not null" json:"name"`
}

// RecoveryPlatformInfrastructure is the one persisted row (always ID 1)
// recording wallet-recovery Branch B's platform-wide infrastructure
// (PLAN.md §15.3/§15.9 Phases 1-2): the recovery-service Safe's
// deployment status and the shared RecoveryGuard contract's deployed
// address. EnsureRecoveryPlatformDeployed is the sole writer, called
// once at server boot - the same idempotent "deploy once, remember
// forever" pattern as User.PrimaryWalletDeployed, just as a singleton
// row instead of a per-user flag since this infrastructure is shared by
// every enrolled wallet rather than owned by any one of them.
type RecoveryPlatformInfrastructure struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// RecoveryServiceAddress is always computed deterministically from
	// GlobalConfig.RecoveryOperatorKeySalts/RecoveryServiceThreshold
	// (see recovery_platform.go's ComputeRecoveryServiceSafeAddress) -
	// recorded here only so a later boot can detect if that config ever
	// changed after deployment (a real operational hazard: it would mean
	// every already-enrolled wallet's second owner no longer matches the
	// currently-configured operator set), not because it can't be
	// recomputed.
	RecoveryServiceAddress  string `gorm:"size:42" json:"recoveryServiceAddress"`
	RecoveryServiceDeployed bool   `gorm:"default:false" json:"recoveryServiceDeployed"`
	// GuardAddress cannot be recomputed the way RecoveryServiceAddress
	// can: RecoveryGuard is deployed via a plain CREATE (network.Client.
	// DeployContract), so its address depends on the deployer key's
	// nonce history at deployment time, not just its constructor
	// argument - this is the one piece of Branch B infrastructure that
	// must be persisted to be found again.
	GuardAddress  string `gorm:"size:42" json:"guardAddress"`
	GuardDeployed bool   `gorm:"default:false" json:"guardDeployed"`
}

// WalletRecoveryLog is Branch B's own audit trail, the wallet-signer-
// recovery equivalent of AccountRecoveryLog above - reusing the same
// tamper-evident cryptoutil.DeriveKey-signed-attestation pattern
// (PLAN.md §15.8) but logging a signer swap on an unchanged address
// rather than a change of address itself.
type WalletRecoveryLog struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Username           string    `gorm:"index;size:32;not null" json:"username"`
	WalletAddress      string    `gorm:"size:42;not null" json:"walletAddress"`
	OldSignerAddress   string    `gorm:"size:42;not null" json:"oldSignerAddress"`
	NewSignerAddress   string    `gorm:"size:42;not null" json:"newSignerAddress"`
	TxHash             string    `gorm:"size:66;not null" json:"txHash"`
	AuthoritySignature string    `gorm:"size:132;not null" json:"authoritySignature"`
	CreatedAt          time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&User{},
	&UserWallet{},
	&SecurityQuestion{},
	&UserSecurityAnswer{},
	&AccountRecoveryEmailVerification{},
	&AccountRecoveryLog{},
	&ReservedName{},
	&RecoveryPlatformInfrastructure{},
	&WalletRecoveryLog{},
}
