// Package services implements the swaps component as a generic,
// router-address-configurable contract-call builder rather than a
// hardcoded integration with one DEX - see PLAN.md §2 and §10. A caller
// supplies the router contract's address, its ABI (or the relevant
// fragment of it), the method to call, and its arguments; this package
// ABI-encodes the call and proposes it as a real Safe transaction against
// the caller's wallet (PLAN.md §13.9's flagged follow-up, closed here -
// see GroupWalletExecutor's doc comment). This works unmodified against
// Uniswap V3's SwapRouter02, Aerodrome, or any other router deployed on
// Base - the project configures which one it wants, this code doesn't
// pick for it.
package services

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"
)

// GroupWalletExecutor is the slice of sharedaccess.Service this component
// needs - see payments.GroupWalletExecutor's doc comment for why this
// exists (this package had the identical bug: building and submitting a
// plain EIP-1559 transaction "from" a wallet address that, once §13 made
// every wallet a Safe smart-contract account, has no private key to ever
// validly sign one with).
type GroupWalletExecutor interface {
	GetGroupByAddress(address string) (*sharedaccessModels.ClosedGroup, error)
	ProposeContractCall(ctx context.Context, proposerAddress string, groupID uint, kind sharedaccessModels.ActionKind, description, contractAddress, valueWei, dataHex, domain, relatedRecordID string) (*sharedaccessModels.PendingAction, error)
	DigestToSign(actionID uint, memberAddress string) (string, error)
	ApproveAction(ctx context.Context, actionID uint, memberAddress, signatureHex string) (*sharedaccessModels.PendingAction, error)
}

type Service struct {
	// SharedAccess resolves a wallet address to its group and actually
	// executes a swap once approved - nil until main.go wires it
	// post-construction, in which case Build/Submit fail closed with a
	// clear error rather than a nil-pointer panic.
	SharedAccess GroupWalletExecutor
	// Alerts reports a rejected submission to an operational channel -
	// defaults to alerting.NoopNotifier (see New); main.go wires the real
	// one in post-construction. See PLAN.md §4.13.
	Alerts alerting.Notifier
}

func New() *Service {
	return &Service{Alerts: alerting.NewNoopNotifier()}
}

// BuildSwapInput is everything needed to encode and propose a swap-router call.
type BuildSwapInput struct {
	WalletAddress string        // the wallet the swap is sourced from (X-Wallet-Address) - a Safe, never an EOA
	SignerAddress string        // the caller's own signer key (X-Signer-Address) - the group member whose approval this proposal needs
	RouterAddress string        // the DEX router contract to call
	RouterABI     string        // JSON ABI (or just the relevant method fragment)
	Method        string        // e.g. "exactInputSingle" for Uniswap V3's SwapRouter02
	Args          []interface{} // decoded JSON args; see network.EncodeContractCall for supported types
	ValueWei      string        // ETH sent with the call, decimal string; "" or "0" for none (e.g. a token-to-token swap)
}

// SwapProposal is what BuildSwapTx returns: the real on-chain SafeTxHash
// digest (see sharedaccess.DigestToSign) the caller must personal_sign
// with their signer key to approve the swap, and the PendingAction id
// that signature approves.
type SwapProposal struct {
	ActionID     uint   `json:"actionId"`
	DigestToSign string `json:"digestToSign"`
}

// BuildSwapTx ABI-encodes the configured router call, proposes it as a
// real Safe transaction against input.WalletAddress, and returns the
// digest input.SignerAddress must sign to approve it.
func (s *Service) BuildSwapTx(ctx context.Context, input BuildSwapInput) (*SwapProposal, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("swaps are not available: shared-access wiring is missing")
	}
	if !validators.IsValidAddress(input.WalletAddress) {
		return nil, apperrors.BadRequest("invalid address")
	}
	if !validators.IsValidAddress(input.RouterAddress) {
		return nil, apperrors.BadRequest("invalid router contract address")
	}
	if input.Method == "" {
		return nil, apperrors.BadRequest("method is required")
	}

	valueWei := input.ValueWei
	if valueWei == "" {
		valueWei = "0"
	}
	if _, ok := new(big.Int).SetString(valueWei, 10); !ok {
		return nil, apperrors.BadRequest("valueWei must be a decimal integer string")
	}

	data, err := network.EncodeContractCall(input.RouterABI, input.Method, input.Args)
	if err != nil {
		return nil, apperrors.BadRequest("failed to encode router call: " + err.Error())
	}

	group, err := s.SharedAccess.GetGroupByAddress(input.WalletAddress)
	if err != nil {
		return nil, err
	}
	dataHex := "0x" + common.Bytes2Hex(data)
	action, err := s.SharedAccess.ProposeContractCall(ctx, input.SignerAddress, group.ID, sharedaccessModels.ActionSwap, "swap", input.RouterAddress, valueWei, dataHex, "", "")
	if err != nil {
		return nil, err
	}
	digest, err := s.SharedAccess.DigestToSign(action.ID, input.SignerAddress)
	if err != nil {
		return nil, err
	}
	return &SwapProposal{ActionID: action.ID, DigestToSign: digest}, nil
}

// SubmitSwap approves actionID (built via BuildSwapTx) with
// signerAddress's personal_sign signature over its digest, executing it
// immediately once the group's approval threshold is met, and returns the
// resulting transaction hash.
func (s *Service) SubmitSwap(ctx context.Context, actionID uint, signerAddress, signature string) (string, error) {
	if s.SharedAccess == nil {
		return "", apperrors.Internal("swaps are not available: shared-access wiring is missing")
	}
	action, err := s.SharedAccess.ApproveAction(ctx, actionID, signerAddress, signature)
	if err != nil {
		_ = s.Alerts.Notify("swap approval rejected: " + err.Error())
		return "", err
	}
	if action.Status != sharedaccessModels.ActionExecuted {
		return "", apperrors.BadRequest("swap is not yet executed (status: " + string(action.Status) + ") - approve again once outstanding approvals are collected")
	}
	return action.TxHash, nil
}
