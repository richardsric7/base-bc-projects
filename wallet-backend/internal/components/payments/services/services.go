package services

import (
	"context"
	"errors"
	"math/big"

	"gorm.io/gorm"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/payments/models"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/validators"
)

// GroupWalletExecutor is the slice of sharedaccess.Service this component
// needs to actually move funds - narrowed to an interface, like every
// other cross-component dependency in this codebase, so tests can supply
// a fake instead of a real sharedaccess.Service. Wired post-construction
// in main.go (paymentsSvc.SharedAccess = sharedaccessSvc), matching the
// paymentsSvc.Alerts/usersSvc.GeoIP pattern.
//
// This exists because of a real, previously-shipped bug (PLAN.md §13.9's
// own flagged follow-up, finally closed here): this package's early
// phases built and submitted a plain EIP-1559 transaction "from" the
// caller's wallet address, which worked when every wallet was a bare EOA.
// Once §13 made every wallet (the primary wallet included) a Safe
// smart-contract account with no private key of its own, that transaction
// could never be validly signed by anyone - the Safe itself has no
// signing key, and any client-supplied signature only ever recovers to
// whatever key actually produced it, never to the Safe's own address.
// sharedaccess already has the real, tested Safe-transaction pipeline
// (propose -> collect an owner's personal_sign approval over the real
// SafeTxHash -> pack and submit execTransaction via a pool relayer) -
// this package now delegates to it rather than duplicating it.
type GroupWalletExecutor interface {
	GetGroupByAddress(address string) (*sharedaccessModels.ClosedGroup, error)
	ProposePayment(ctx context.Context, proposerAddress string, groupID uint, description, recipient, tokenAddress, amount, domain, relatedRecordID string) (*sharedaccessModels.PendingAction, error)
	DigestToSign(actionID uint, memberAddress string) (string, error)
	ApproveAction(ctx context.Context, actionID uint, memberAddress, signatureHex string) (*sharedaccessModels.PendingAction, error)
}

// RecipientResolver turns a payment recipient identifier - an address,
// username, email, or wallet alias (users.UserWallet's own doc comment) -
// into the address to actually pay, matching the original's own
// GetUser/GetWallet resolution. Narrowed to an interface like
// GroupWalletExecutor so tests can supply a fake instead of a real
// users.Service.
type RecipientResolver interface {
	ResolveRecipient(identifier string) (string, error)
}

type Service struct {
	DB *gorm.DB
	// SharedAccess resolves a wallet address to its group and actually
	// executes a payment once approved - nil until main.go wires it
	// post-construction, in which case Build/Submit fail closed with a
	// clear error rather than a nil-pointer panic.
	SharedAccess GroupWalletExecutor
	// Recipients resolves BuildPaymentTx's `to` parameter before it's
	// validated as an address - nil until main.go wires it
	// post-construction (paymentsSvc.Recipients = usersSvc), in which
	// case Build fails closed the same way a missing SharedAccess does.
	Recipients RecipientResolver
	// Alerts reports a rejected submission to an operational channel -
	// defaults to alerting.NoopNotifier (see New); main.go wires the real
	// one in post-construction. See PLAN.md §4.13.
	Alerts alerting.Notifier
}

func New(db *gorm.DB) *Service {
	return &Service{DB: db, Alerts: alerting.NewNoopNotifier()}
}

// PaymentProposal is what BuildPaymentTx returns: the real on-chain
// SafeTxHash digest (see sharedaccess.DigestToSign) the caller must
// personal_sign with their signer key to approve the payment, and the
// PendingAction id that signature approves. ResolvedAddress is what `to`
// actually resolved to (RecipientResolver) - always present, even when
// `to` was already a bare address, so a client can show a "sending to X"
// confirmation before the caller commits to signing, the way the
// original's own payment flow surfaced which alias/wallet an email or
// username resolved to.
type PaymentProposal struct {
	ActionID        uint   `json:"actionId"`
	DigestToSign    string `json:"digestToSign"`
	ResolvedAddress string `json:"resolvedAddress"`
}

// BuildPaymentTx proposes a native-ETH or ERC-20 transfer from walletAddress
// (a group's Safe address - the primary wallet or a sub-wallet) and returns
// the digest signerAddress must sign to approve it. Pass an empty
// tokenAddress for a native ETH transfer. amount is a decimal string in the
// asset's smallest unit (wei for ETH, the token's base unit for an ERC-20)
// to avoid floating-point precision loss. signerAddress is the caller's own
// signer key - the group member whose approval this proposal needs, per
// sharedaccess's group-membership model (PLAN.md §13.4) - not
// walletAddress itself, which never has a private key of its own. to is
// resolved through RecipientResolver before validation, so it may be an
// address, username, email, or wallet alias. memo is the original's own
// optional free-text payment reference (PLAN.md §23) - used as the
// sharedaccess proposal's own description (what an approver sees) when
// non-empty, falling back to the literal "payment" otherwise.
func (s *Service) BuildPaymentTx(ctx context.Context, walletAddress, signerAddress, to, tokenAddress, amount, memo string) (*PaymentProposal, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("payments are not available: shared-access wiring is missing")
	}
	if s.Recipients == nil {
		return nil, apperrors.Internal("payments are not available: recipient resolution wiring is missing")
	}
	resolvedTo, err := s.Recipients.ResolveRecipient(to)
	if err != nil {
		return nil, err
	}
	if !validators.IsValidAddress(walletAddress) || !validators.IsValidAddress(resolvedTo) {
		return nil, apperrors.BadRequest("invalid address")
	}
	if _, ok := new(big.Int).SetString(amount, 10); !ok {
		return nil, apperrors.BadRequest("amount must be a decimal integer string in the asset's smallest unit")
	}

	description := "payment"
	if memo != "" {
		description = memo
	}
	group, err := s.SharedAccess.GetGroupByAddress(walletAddress)
	if err != nil {
		return nil, err
	}
	action, err := s.SharedAccess.ProposePayment(ctx, signerAddress, group.ID, description, resolvedTo, tokenAddress, amount, "", "")
	if err != nil {
		return nil, err
	}
	digest, err := s.SharedAccess.DigestToSign(action.ID, signerAddress)
	if err != nil {
		return nil, err
	}
	return &PaymentProposal{ActionID: action.ID, DigestToSign: digest, ResolvedAddress: resolvedTo}, nil
}

// SubmitPayment approves actionID (built via BuildPaymentTx) with
// signerAddress's personal_sign signature over its digest, executing it
// immediately once the group's approval threshold is met - always true
// for an ordinary primary-wallet payment (threshold 1, sole owner), only
// sometimes for a shared-access sub-wallet with other approvers still
// outstanding, in which case this returns an error rather than a history
// record: an unexecuted payment isn't a "payment" yet. Because signing can
// happen long after Build (PLAN.md §3 - the whole point of offline
// signing), an app's natural retry-on-reconnect behavior means the same
// approval may be submitted here more than once; resubmitting the same
// idempotencyKey returns the original record rather than erroring or
// creating a duplicate history entry. destination/tokenAddress/amount/memo
// are carried through from the original Build call purely to populate the
// history record's display fields - the actual transfer these authorize
// was already fixed at proposal time and cannot be changed here.
func (s *Service) SubmitPayment(ctx context.Context, idempotencyKey string, actionID uint, signerAddress, signature, fromAddress, toAddress, tokenAddress, amount, memo string) (*models.PaymentHistory, error) {
	var existing models.PaymentHistory
	err := s.DB.Where("idempotency_key = ?", idempotencyKey).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for a previous submission")
	}
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("payments are not available: shared-access wiring is missing")
	}

	action, err := s.SharedAccess.ApproveAction(ctx, actionID, signerAddress, signature)
	if err != nil {
		_ = s.Alerts.Notify("payment approval rejected: " + err.Error())
		return nil, err
	}
	if action.Status != sharedaccessModels.ActionExecuted {
		return nil, apperrors.BadRequest("payment is not yet executed (status: " + string(action.Status) + ") - approve again once outstanding approvals are collected")
	}

	record := models.PaymentHistory{
		IdempotencyKey: idempotencyKey,
		FromAddress:    fromAddress,
		ToAddress:      toAddress,
		TokenAddress:   tokenAddress,
		Amount:         amount,
		Memo:           memo,
		TxHash:         action.TxHash,
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return nil, apperrors.Internal("payment submitted but failed to record history")
	}
	return &record, nil
}

// History returns the payments an address has sent or received, most
// recent first.
func (s *Service) History(address string) ([]models.PaymentHistory, error) {
	var history []models.PaymentHistory
	err := s.DB.Where("from_address = ? OR to_address = ?", address, address).
		Order("created_at DESC").
		Find(&history).Error
	if err != nil {
		return nil, apperrors.Internal("failed to load payment history")
	}
	return history, nil
}
