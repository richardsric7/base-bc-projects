// Package services implements shared/multi-party wallet access: a group of
// members controls one Base address via a threshold of enforced
// approvals. See PLAN.md §2 for the feature's original design and §13.3-
// §13.6 for why and how it was migrated onto a real Gnosis Safe
// smart-contract account rather than a server-derived custodial key.
//
// A group's address is a genuine Safe, deployed on creation
// (safe.ComputeProxyAddress/EncodeCreateProxyWithNonceCalldata) with an
// owner set drawn from members holding APPROVER or INITIATOR_APPROVER
// (models.CanApprove) - the members whose signatures the Safe contract
// itself, not this server, will require to reach the group's threshold.
// Every member address naming another platform user must be that user's
// primary wallet Safe address, never their raw signer EOA
// (validateSafeOwnerCandidate) - PLAN.md §13.4's nested-EIP-1271 design
// depends on this so a member's own future key rotation never requires
// touching a wallet they merely participate in.
//
// PLAN.md §13.10 Phase 3 stops at creation: CreateGroup deploys a real
// Safe, but ApproveAction's off-chain approval-recording and
// executeAction's actual on-chain submission are not yet rewired to
// match (see executeAction's own doc comment) - that lands in Phase 4
// (the relayer submission path) and Phase 6 (per-Safe nonce reservation),
// which is also where members' approvals start signing the real
// safe.SafeTxHash instead of today's off-chain-only descriptive message.
package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/sharedaccess/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
	"wallet-backend/internal/safe"
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
	DB         *gorm.DB
	Blockchain BlockchainClient
	// DeployerKeySalt seeds the single platform key that pays gas to
	// deploy every group's Safe - the same permissionless-factory-call
	// role as users.Service's own deployer key (sharedconfig.
	// SafeDeployerKeySalt is passed to both), reused rather than
	// duplicated since deploying a Safe never requires any authority
	// over the wallet it deploys.
	DeployerKeySalt string
}

func New(db *gorm.DB, blockchain BlockchainClient, deployerKeySalt string) *Service {
	return &Service{DB: db, Blockchain: blockchain, DeployerKeySalt: deployerKeySalt}
}

// deriveSafeDeployerKey derives the key that pays gas to deploy a group's
// Safe - see the Service.DeployerKeySalt field doc.
func (s *Service) deriveSafeDeployerKey() (*ecdsa.PrivateKey, error) {
	key, err := cryptoutil.DeriveKey(s.DeployerKeySalt + "|safe-deployer")
	if err != nil {
		return nil, apperrors.Internal("failed to derive the Safe deployer key")
	}
	return key, nil
}

// groupSafeSaltNonce returns a fresh random CREATE2 saltNonce for a new
// group's Safe. Unlike a primary wallet (PLAN.md §13.10 Phase 2, saltNonce
// always 0 - safe because each user's initializer already differs by
// naming a different sole owner), two different groups can easily share
// an identical owner set and threshold (e.g. the same user creating two
// single-owner sub-wallets back to back), which would produce the exact
// same initializer and, without a varying salt, the exact same address -
// so this must be unpredictable per group, not fixed.
func groupSafeSaltNonce() (*big.Int, error) {
	nonce, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 256))
	if err != nil {
		return nil, apperrors.Internal("failed to generate a Safe salt nonce")
	}
	return nonce, nil
}

// validateSafeOwnerCandidate enforces PLAN.md §13.4's naming rule for any
// member address that will become a Safe owner (CanApprove(role)):
// rejecting a registered user's raw signer EOA outright (they must be
// named by their primary wallet Safe address instead, so their own future
// recovery-driven key rotation never requires touching this group), and
// rejecting a registered user's primary wallet that exists but hasn't
// been deployed on-chain yet (PLAN.md §13.11 - EIP-1271 resolution needs
// code at that address, so a not-yet-deployed owner would make the whole
// group permanently unusable until they separately deploy). An address
// matching neither case - an external EOA, a not-yet-registered address,
// or an already-deployed primary wallet - is accepted as-is.
func (s *Service) validateSafeOwnerCandidate(address string) error {
	var bySigner usersModels.User
	err := s.DB.Where("LOWER(signer_address) = LOWER(?)", address).First(&bySigner).Error
	if err == nil {
		return apperrors.BadRequest("member " + address + " is a registered user's signer key, not their primary wallet address (" + bySigner.Address + ") - name the primary wallet instead")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.Internal("failed to validate member address")
	}

	var byAddress usersModels.User
	err = s.DB.Where("LOWER(address) = LOWER(?)", address).First(&byAddress).Error
	if err == nil && !byAddress.PrimaryWalletDeployed {
		return apperrors.Conflict("member " + address + " is a registered user's primary wallet, but it has not been deployed on-chain yet")
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.Internal("failed to validate member address")
	}
	return nil
}

// MemberInput is one member to add when creating a group.
type MemberInput struct {
	Address string
	Role    models.GroupRole
}

// CreateGroup creates a new Base wallet controlled by members: a real Safe
// (owners = every member holding APPROVER or INITIATOR_APPROVER,
// threshold = threshold), deployed on-chain immediately so the wallet is
// usable right away, plus a ClosedGroup/GroupMember row recording every
// member regardless of role (VIEW_ONLY and plain INITIATOR members are
// recorded for this application's own authorization checks even though
// they hold no on-chain signing power). A sub-wallet (PLAN.md §13.6) is
// simply the single-member case: one member, role INITIATOR_APPROVER,
// threshold 1, address the caller's own primary wallet.
func (s *Service) CreateGroup(ctx context.Context, name string, threshold int, members []MemberInput) (*models.ClosedGroup, error) {
	if name == "" {
		return nil, apperrors.BadRequest("name is required")
	}
	if threshold < 1 {
		return nil, apperrors.BadRequest("threshold must be at least 1")
	}
	var owners []common.Address
	for _, m := range members {
		if !validators.IsValidAddress(m.Address) {
			return nil, apperrors.BadRequest("invalid member address: " + m.Address)
		}
		switch m.Role {
		case models.RoleInitiator, models.RoleApprover, models.RoleViewOnly, models.RoleInitiatorApprover:
		default:
			return nil, apperrors.BadRequest("invalid role for " + m.Address)
		}
		if models.CanApprove(m.Role) {
			if err := s.validateSafeOwnerCandidate(m.Address); err != nil {
				return nil, err
			}
			owners = append(owners, common.HexToAddress(m.Address))
		}
	}
	if threshold > len(owners) {
		return nil, apperrors.BadRequest(fmt.Sprintf("threshold (%d) exceeds the number of approvers (%d)", threshold, len(owners)))
	}

	initializer, err := safe.EncodeSetupCalldata(owners, big.NewInt(int64(threshold)))
	if err != nil {
		return nil, apperrors.Internal("failed to encode group wallet setup calldata")
	}
	saltNonce, err := groupSafeSaltNonce()
	if err != nil {
		return nil, err
	}
	groupAddress := safe.ComputeProxyAddress(safe.SingletonAddress, initializer, saltNonce)

	deployerKey, err := s.deriveSafeDeployerKey()
	if err != nil {
		return nil, err
	}
	deployCalldata, err := safe.EncodeCreateProxyWithNonceCalldata(safe.SingletonAddress, initializer, saltNonce)
	if err != nil {
		return nil, apperrors.Internal("failed to encode group wallet deployment calldata")
	}
	factoryAddr := safe.ProxyFactoryAddress
	if _, err := s.Blockchain.SignAndSubmitTx(ctx, deployerKey, &factoryAddr, big.NewInt(0), deployCalldata, nil); err != nil {
		return nil, apperrors.Internal("failed to deploy group wallet: " + err.Error())
	}

	addressHex := groupAddress.Hex()
	group := models.ClosedGroup{Name: name, Purpose: models.PurposeWalletAccess, Threshold: threshold, Address: &addressHex}
	txErr := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&group).Error; err != nil {
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
		return nil, apperrors.Internal("failed to record deployed group wallet")
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
	if !models.CanInitiate(role) {
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
	if !models.CanApprove(role) {
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
// threshold. Before PLAN.md §13's Safe migration this derived the
// group's custodial key and sent the transaction directly from it; since
// Phase 3 made a group's Address a real Safe, that key has no
// relationship to the group's actual funds or on-chain authority at all
// (it isn't a Safe owner, and even if it somehow held ETH, spending from
// it would never move the Safe's own balance). Rather than let that
// silently attempt - and confusingly fail deep inside an RPC call, or
// worse, appear to "succeed" against the wrong account - this fails
// immediately and explicitly: real submission (building the Safe
// execTransaction call, packing members' real safe.SafeTxHash signatures
// via safe.PackSignatures, and relaying it through a funded key) is
// PLAN.md §13.10 Phase 4's job, built on Phase 6's per-Safe nonce
// reservation so concurrent proposals against the same Safe can't race.
// Until then, approvals still record correctly (ApproveAction's
// signature check is an off-chain gate, unrelated to Safe mechanics) and
// tallyAndMaybeExecute still recognizes when a threshold is reached - only
// this last step, actually moving funds, is not yet available.
func (s *Service) executeAction(_ context.Context, action *models.PendingAction) (*models.PendingAction, error) {
	return nil, apperrors.Internal("this wallet's threshold has been met, but on-chain execution via a real Safe transaction is not implemented yet - see PLAN.md §13.10 Phases 4 and 6")
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
	if !models.CanApprove(role) {
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
