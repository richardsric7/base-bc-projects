package services

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/assets/models"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"
)

// GroupWalletExecutor is the slice of sharedaccess.Service this component
// needs - see payments.GroupWalletExecutor's doc comment for why this
// exists (this package had the identical bug: building and submitting a
// plain EIP-1559 approve() transaction "from" a wallet address that, once
// §13 made every wallet a Safe smart-contract account, has no private key
// to ever validly sign one with).
type GroupWalletExecutor interface {
	GetGroupByAddress(address string) (*sharedaccessModels.ClosedGroup, error)
	ProposeContractCall(ctx context.Context, proposerAddress string, groupID uint, kind sharedaccessModels.ActionKind, description, contractAddress, valueWei, dataHex, domain, relatedRecordID string) (*sharedaccessModels.PendingAction, error)
	DigestToSign(actionID uint, memberAddress string) (string, error)
	ApproveAction(ctx context.Context, actionID uint, memberAddress, signatureHex string) (*sharedaccessModels.PendingAction, error)
}

type Service struct {
	DB         *gorm.DB
	Blockchain *network.Client
	// SharedAccess resolves a wallet address to its group and actually
	// executes an approval once approved - nil until main.go wires it
	// post-construction, in which case BuildApproveTx/SubmitApprove fail
	// closed with a clear error rather than a nil-pointer panic.
	SharedAccess GroupWalletExecutor
}

func New(db *gorm.DB, blockchain *network.Client) *Service {
	return &Service{DB: db, Blockchain: blockchain}
}

// ListCurated returns the active entries in the curated-token catalog.
func (s *Service) ListCurated() ([]models.CuratedToken, error) {
	var tokens []models.CuratedToken
	if err := s.DB.Where("is_active = ?", true).Find(&tokens).Error; err != nil {
		return nil, apperrors.Internal("failed to load curated tokens")
	}
	return tokens, nil
}

// Balance returns an address's balance of either native ETH (pass an empty
// tokenAddress) or a specific ERC-20 token, in the smallest unit (wei / the
// token's base unit) as a decimal string.
func (s *Service) Balance(ctx context.Context, address, tokenAddress string) (string, error) {
	if !validators.IsValidAddress(address) {
		return "", apperrors.BadRequest("invalid address")
	}
	if tokenAddress == "" {
		balance, err := s.Blockchain.NativeBalance(ctx, address)
		if err != nil {
			return "", apperrors.Internal("failed to read balance: " + err.Error())
		}
		return balance.String(), nil
	}
	if !validators.IsValidAddress(tokenAddress) {
		return "", apperrors.BadRequest("invalid token contract address")
	}
	balance, err := s.Blockchain.ERC20BalanceOf(ctx, tokenAddress, address)
	if err != nil {
		return "", apperrors.Internal("failed to read balance: " + err.Error())
	}
	return balance.String(), nil
}

// ApproveProposal is what BuildApproveTx returns: the real on-chain
// SafeTxHash digest (see sharedaccess.DigestToSign) the caller must
// personal_sign with their signer key to approve the token approval, and
// the PendingAction id that signature approves.
type ApproveProposal struct {
	ActionID     uint   `json:"actionId"`
	DigestToSign string `json:"digestToSign"`
}

// BuildApproveTx proposes an ERC-20 approve(spender, amount) call as a real
// Safe transaction against walletAddress - the base template's substitute
// for Stellar's trustline build/submit, see PLAN.md §5.1 (updated by
// PLAN.md §17 for the Safe-based primary wallet). amount is a decimal
// string (the token's base unit, not a human-readable amount) to avoid
// floating-point precision loss. signerAddress is the caller's own signer
// key - the group member whose approval this proposal needs - not
// walletAddress itself, which never has a private key of its own.
func (s *Service) BuildApproveTx(ctx context.Context, walletAddress, signerAddress, tokenAddress, spender, amount string) (*ApproveProposal, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("approvals are not available: shared-access wiring is missing")
	}
	if !validators.IsValidAddress(walletAddress) || !validators.IsValidAddress(spender) {
		return nil, apperrors.BadRequest("invalid address")
	}
	if !validators.IsValidAddress(tokenAddress) {
		return nil, apperrors.BadRequest("invalid token contract address")
	}
	amountWei, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return nil, apperrors.BadRequest("amount must be a decimal integer string in the token's base unit")
	}

	data, err := network.EncodeERC20Approve(spender, amountWei)
	if err != nil {
		return nil, apperrors.BadRequest(err.Error())
	}

	group, err := s.SharedAccess.GetGroupByAddress(walletAddress)
	if err != nil {
		return nil, err
	}
	dataHex := "0x" + common.Bytes2Hex(data)
	action, err := s.SharedAccess.ProposeContractCall(ctx, signerAddress, group.ID, sharedaccessModels.ActionContractCall, "approve", tokenAddress, "0", dataHex, "", "")
	if err != nil {
		return nil, err
	}
	digest, err := s.SharedAccess.DigestToSign(action.ID, signerAddress)
	if err != nil {
		return nil, err
	}
	return &ApproveProposal{ActionID: action.ID, DigestToSign: digest}, nil
}

// SubmitApprove approves actionID (built via BuildApproveTx) with
// signerAddress's personal_sign signature over its digest, executing it
// immediately once the group's approval threshold is met, and returns the
// resulting transaction hash.
func (s *Service) SubmitApprove(ctx context.Context, actionID uint, signerAddress, signature string) (string, error) {
	if s.SharedAccess == nil {
		return "", apperrors.Internal("approvals are not available: shared-access wiring is missing")
	}
	action, err := s.SharedAccess.ApproveAction(ctx, actionID, signerAddress, signature)
	if err != nil {
		return "", err
	}
	if action.Status != sharedaccessModels.ActionExecuted {
		return "", apperrors.BadRequest("approval is not yet executed (status: " + string(action.Status) + ") - approve again once outstanding approvals are collected")
	}
	return action.TxHash, nil
}
