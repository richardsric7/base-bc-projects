// Package services implements shared/multi-party wallet access: a group of
// members controls one Base address via a threshold of off-chain
// approvals rather than any single private key. See PLAN.md §2 (the
// "native multi-signature" row) for why this design was chosen over a
// smart-contract wallet, and §4.2 for the full route/model mapping.
//
// The group's Base address is controlled by a key derived from its ID via
// cryptoutil.DeriveKey - it never exists as a value any member (or this
// server's operator) can casually access, only as a deterministic function
// this process can recompute. Members prove authorization to move that
// wallet by each signing a description of the exact proposed action with
// their own wallet key; once enough distinct members have signed, the
// server derives the group key, builds the real transaction, signs it, and
// submits it.
package services

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"
)

// BlockchainClient is the slice of *network.Client this service needs -
// narrowed to an interface so tests can exercise the approval-threshold
// execution path with a fake instead of a live Base RPC connection.
type BlockchainClient interface {
	SignAndSubmitTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
	NativeBalance(ctx context.Context, address string) (*big.Int, error)
	ERC20BalanceOf(ctx context.Context, tokenAddress, owner string) (*big.Int, error)
}

type Service struct {
	DB           *gorm.DB
	Blockchain   BlockchainClient
	GroupKeySalt string
}

func New(db *gorm.DB, blockchain BlockchainClient, groupKeySalt string) *Service {
	return &Service{DB: db, Blockchain: blockchain, GroupKeySalt: groupKeySalt}
}

// deriveGroupKey recomputes the group's controlling key. Deterministic in
// the group's ID, so the key never needs to be stored - only ever
// recomputed at the moment it's needed to sign.
func (s *Service) deriveGroupKey(groupID uint) (*ecdsa.PrivateKey, error) {
	key, err := cryptoutil.DeriveKey(s.GroupKeySalt + "|shared-access-group|" + strconv.FormatUint(uint64(groupID), 10))
	if err != nil {
		return nil, apperrors.Internal("failed to derive group signing key")
	}
	return key, nil
}

// MemberInput is one member to add when creating a group.
type MemberInput struct {
	Address string
	Role    models.GroupRole
}

// CreateGroup creates a new shared-access wallet: a group row (whose
// address is derived from its own ID once it exists), plus its initial
// membership list.
func (s *Service) CreateGroup(name string, threshold int, members []MemberInput) (*models.ClosedGroup, error) {
	if name == "" {
		return nil, apperrors.BadRequest("name is required")
	}
	if threshold < 1 {
		return nil, apperrors.BadRequest("threshold must be at least 1")
	}
	approverCount := 0
	for _, m := range members {
		if !validators.IsValidAddress(m.Address) {
			return nil, apperrors.BadRequest("invalid member address: " + m.Address)
		}
		switch m.Role {
		case models.RoleInitiator, models.RoleApprover, models.RoleViewOnly:
		default:
			return nil, apperrors.BadRequest("invalid role for " + m.Address)
		}
		if m.Role == models.RoleApprover {
			approverCount++
		}
	}
	if threshold > approverCount {
		return nil, apperrors.BadRequest(fmt.Sprintf("threshold (%d) exceeds the number of approvers (%d)", threshold, approverCount))
	}

	group := models.ClosedGroup{Name: name, Purpose: models.PurposeWalletAccess, Threshold: threshold}
	txErr := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		key, err := s.deriveGroupKey(group.ID)
		if err != nil {
			return err
		}
		address := crypto.PubkeyToAddress(key.PublicKey).Hex()
		group.Address = &address
		if err := tx.Model(&group).Update("address", group.Address).Error; err != nil {
			return err
		}
		for _, m := range members {
			row := models.GroupMember{GroupID: group.ID, MemberAddress: m.Address, Role: m.Role}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		return nil, apperrors.Internal("failed to create group")
	}
	return &group, nil
}

// GetGroup fetches a group by ID.
func (s *Service) GetGroup(groupID uint) (*models.ClosedGroup, error) {
	var group models.ClosedGroup
	if err := s.DB.First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("group not found")
		}
		return nil, apperrors.Internal("failed to load group")
	}
	return &group, nil
}

// memberRole returns the caller's role on a group, or a 403 if they aren't a member.
func (s *Service) memberRole(groupID uint, address string) (models.GroupRole, error) {
	var member models.GroupMember
	err := s.DB.Where("group_id = ? AND member_address = ?", groupID, address).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", apperrors.Forbidden("you are not a member of this group")
		}
		return "", apperrors.Internal("failed to load group membership")
	}
	return member.Role, nil
}

// ProposePayment proposes a native-ETH or ERC-20 payment from the group's
// wallet. Only an INITIATOR may propose.
func (s *Service) ProposePayment(proposerAddress string, groupID uint, description, recipient, tokenAddress, amount string) (*models.PendingAction, error) {
	group, err := s.requireInitiator(groupID, proposerAddress)
	if err != nil {
		return nil, err
	}
	if !validators.IsValidAddress(recipient) {
		return nil, apperrors.BadRequest("invalid recipient address")
	}
	amountValue, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return nil, apperrors.BadRequest("amount must be a decimal integer string in the asset's smallest unit")
	}

	var to, tokenAddr, value, data string
	if tokenAddress == "" {
		to, value, data = recipient, amount, "0x"
	} else {
		if !validators.IsValidAddress(tokenAddress) {
			return nil, apperrors.BadRequest("invalid token contract address")
		}
		callData, err := network.EncodeERC20Transfer(recipient, amountValue)
		if err != nil {
			return nil, apperrors.BadRequest(err.Error())
		}
		to, tokenAddr, value, data = tokenAddress, tokenAddress, "0", "0x"+common.Bytes2Hex(callData)
	}

	return s.createPendingAction(group, proposerAddress, models.ActionPayment, description, to, tokenAddr, value, data)
}

// ProposeContractCall proposes an arbitrary contract call (e.g. a swap
// router call built the same way internal/components/swaps builds one) from
// the group's wallet. Only an INITIATOR may propose.
func (s *Service) ProposeContractCall(proposerAddress string, groupID uint, kind models.ActionKind, description, contractAddress, valueWei, dataHex string) (*models.PendingAction, error) {
	group, err := s.requireInitiator(groupID, proposerAddress)
	if err != nil {
		return nil, err
	}
	if !validators.IsValidAddress(contractAddress) {
		return nil, apperrors.BadRequest("invalid contract address")
	}
	if valueWei == "" {
		valueWei = "0"
	}
	if _, ok := new(big.Int).SetString(valueWei, 10); !ok {
		return nil, apperrors.BadRequest("valueWei must be a decimal integer string")
	}
	if !strings.HasPrefix(dataHex, "0x") {
		return nil, apperrors.BadRequest("data must be 0x-prefixed hex")
	}

	return s.createPendingAction(group, proposerAddress, kind, description, contractAddress, "", valueWei, dataHex)
}

func (s *Service) requireInitiator(groupID uint, address string) (*models.ClosedGroup, error) {
	group, err := s.GetGroup(groupID)
	if err != nil {
		return nil, err
	}
	if group.Disabled {
		return nil, apperrors.Conflict("this group has been disabled")
	}
	role, err := s.memberRole(groupID, address)
	if err != nil {
		return nil, err
	}
	if role != models.RoleInitiator {
		return nil, apperrors.Forbidden("only an INITIATOR may propose an action")
	}
	return group, nil
}

func (s *Service) createPendingAction(group *models.ClosedGroup, proposer string, kind models.ActionKind, description, to, tokenAddress, value, data string) (*models.PendingAction, error) {
	action := models.PendingAction{
		GroupID:           group.ID,
		ProposerAddress:   proposer,
		Kind:              kind,
		Description:       description,
		To:                to,
		TokenAddress:      tokenAddress,
		Value:             value,
		Data:              data,
		RequiredApprovals: group.Threshold,
		Status:            models.ActionPending,
	}
	if err := s.DB.Create(&action).Error; err != nil {
		return nil, apperrors.Internal("failed to propose action")
	}
	return &action, nil
}

// canonicalActionMessage is exactly what a member signs to approve an
// action - reconstructed identically by the server at verification time, so
// there is no ambiguity about what was approved.
func canonicalActionMessage(action *models.PendingAction) string {
	return fmt.Sprintf(
		"wallet-backend shared action #%d\ngroup: %d\nkind: %s\nto: %s\ntoken: %s\nvalue: %s\ndata: %s",
		action.ID, action.GroupID, action.Kind, action.To, action.TokenAddress, action.Value, action.Data,
	)
}

// CanonicalActionMessage exposes the exact message a member must sign to
// approve actionID, so a client can construct the correct signature.
func (s *Service) CanonicalActionMessage(actionID uint) (string, error) {
	action, err := s.GetAction(actionID)
	if err != nil {
		return "", err
	}
	return canonicalActionMessage(action), nil
}

// GetAction fetches one pending action.
func (s *Service) GetAction(actionID uint) (*models.PendingAction, error) {
	var action models.PendingAction
	if err := s.DB.First(&action, actionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("action not found")
		}
		return nil, apperrors.Internal("failed to load action")
	}
	return &action, nil
}

// ListPendingForMember returns every PENDING action on a group the caller
// has APPROVER (or INITIATOR) access to.
func (s *Service) ListPendingForMember(memberAddress string) ([]models.PendingAction, error) {
	var groupIDs []uint
	err := s.DB.Model(&models.GroupMember{}).
		Where("member_address = ? AND role IN ?", memberAddress, []models.GroupRole{models.RoleInitiator, models.RoleApprover}).
		Pluck("group_id", &groupIDs).Error
	if err != nil {
		return nil, apperrors.Internal("failed to load group memberships")
	}
	if len(groupIDs) == 0 {
		return []models.PendingAction{}, nil
	}
	var actions []models.PendingAction
	err = s.DB.Where("group_id IN ? AND status = ?", groupIDs, models.ActionPending).
		Order("created_at DESC").Find(&actions).Error
	if err != nil {
		return nil, apperrors.Internal("failed to load pending actions")
	}
	return actions, nil
}

// ApproveAction records memberAddress's signed approval of actionID and, if
// that brings the tally to the group's threshold, executes the action:
// derives the group's key, signs, and submits the real transaction.
func (s *Service) ApproveAction(ctx context.Context, actionID uint, memberAddress, signatureHex string) (*models.PendingAction, error) {
	action, err := s.GetAction(actionID)
	if err != nil {
		return nil, err
	}
	if action.Status != models.ActionPending {
		return nil, apperrors.Conflict("this action is no longer pending")
	}

	role, err := s.memberRole(action.GroupID, memberAddress)
	if err != nil {
		return nil, err
	}
	if role != models.RoleApprover {
		return nil, apperrors.Forbidden("only an APPROVER may approve an action")
	}

	var existing models.PendingActionApproval
	err = s.DB.Where("pending_action_id = ? AND member_address = ?", actionID, memberAddress).First(&existing).Error
	if err == nil {
		// This member already approved. The action is still PENDING (checked
		// above), which can only mean a prior execution attempt failed after
		// the threshold was already met (e.g. a transient RPC error) - retry
		// execution rather than rejecting this as a duplicate, since without
		// this every approver having already signed once would permanently
		// strand the action with no way to move it forward.
		return s.tallyAndMaybeExecute(ctx, action)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for an existing approval")
	}

	if !strings.HasPrefix(signatureHex, "0x") {
		return nil, apperrors.BadRequest("signature must be 0x-prefixed hex")
	}
	sigBytes := common.FromHex(signatureHex)
	valid, err := cryptoutil.VerifyPersonalSign(canonicalActionMessage(action), sigBytes, common.HexToAddress(memberAddress))
	if err != nil {
		return nil, apperrors.BadRequest("invalid signature: " + err.Error())
	}
	if !valid {
		return nil, apperrors.Unauthorized("signature does not match the approving member's address")
	}

	if err := s.DB.Create(&models.PendingActionApproval{PendingActionID: actionID, MemberAddress: memberAddress, Signature: signatureHex}).Error; err != nil {
		return nil, apperrors.Internal("failed to record approval")
	}

	return s.tallyAndMaybeExecute(ctx, action)
}

// tallyAndMaybeExecute counts recorded approvals and executes the action if
// the group's threshold has been met. Called both right after a new
// approval is recorded and when retrying a previously failed execution
// attempt (see ApproveAction's already-approved branch above).
func (s *Service) tallyAndMaybeExecute(ctx context.Context, action *models.PendingAction) (*models.PendingAction, error) {
	var approvalCount int64
	if err := s.DB.Model(&models.PendingActionApproval{}).Where("pending_action_id = ?", action.ID).Count(&approvalCount).Error; err != nil {
		return nil, apperrors.Internal("failed to tally approvals")
	}
	if int(approvalCount) < action.RequiredApprovals {
		return action, nil
	}
	return s.executeAction(ctx, action)
}

// executeAction is called once an action has reached its approval
// threshold: it derives the group's key and performs the real,
// server-signed transaction.
func (s *Service) executeAction(ctx context.Context, action *models.PendingAction) (*models.PendingAction, error) {
	groupKey, err := s.deriveGroupKey(action.GroupID)
	if err != nil {
		return nil, err
	}

	value, ok := new(big.Int).SetString(action.Value, 10)
	if !ok {
		return nil, apperrors.Internal("stored action has an invalid value")
	}
	data := common.FromHex(action.Data)
	toAddr := common.HexToAddress(action.To)

	hash, err := s.Blockchain.SignAndSubmitTx(ctx, groupKey, &toAddr, value, data, nil)
	if err != nil {
		return nil, apperrors.Internal("action approved but execution failed: " + err.Error())
	}

	action.Status = models.ActionExecuted
	action.TxHash = hash
	if err := s.DB.Save(action).Error; err != nil {
		return nil, apperrors.Internal("action executed (tx " + hash + ") but failed to record the result")
	}
	return action, nil
}

// RejectAction marks a pending action rejected. Only an APPROVER may reject,
// and a reason is required (matching the original's requirement).
func (s *Service) RejectAction(actionID uint, memberAddress, reason string) (*models.PendingAction, error) {
	if reason == "" {
		return nil, apperrors.BadRequest("a rejection reason is required")
	}
	action, err := s.GetAction(actionID)
	if err != nil {
		return nil, err
	}
	if action.Status != models.ActionPending {
		return nil, apperrors.Conflict("this action is no longer pending")
	}
	role, err := s.memberRole(action.GroupID, memberAddress)
	if err != nil {
		return nil, err
	}
	if role != models.RoleApprover {
		return nil, apperrors.Forbidden("only an APPROVER may reject an action")
	}

	action.Status = models.ActionRejected
	action.RejectionReason = reason
	if err := s.DB.Save(action).Error; err != nil {
		return nil, apperrors.Internal("failed to reject action")
	}
	return action, nil
}

// Balance returns a group wallet's native ETH balance (empty tokenAddress)
// or a specific ERC-20 balance. Any member (including VIEW_ONLY) may check it.
func (s *Service) Balance(ctx context.Context, groupID uint, callerAddress, tokenAddress string) (string, error) {
	if _, err := s.memberRole(groupID, callerAddress); err != nil {
		return "", err
	}
	group, err := s.GetGroup(groupID)
	if err != nil {
		return "", err
	}
	if group.Address == nil {
		return "", apperrors.Internal("group has no wallet address")
	}
	if tokenAddress == "" {
		balance, err := s.Blockchain.NativeBalance(ctx, *group.Address)
		if err != nil {
			return "", apperrors.Internal("failed to read balance: " + err.Error())
		}
		return balance.String(), nil
	}
	balance, err := s.Blockchain.ERC20BalanceOf(ctx, tokenAddress, *group.Address)
	if err != nil {
		return "", apperrors.Internal("failed to read balance: " + err.Error())
	}
	return balance.String(), nil
}
