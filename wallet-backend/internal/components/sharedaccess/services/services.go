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
// PLAN.md §13.10 Phase 4 makes execution real: once a PendingAction's
// threshold is met, executeAction builds the actual Safe execTransaction
// call, packs members' real safe.SafeTxHash-derived signatures via
// safe.PackSignatures (nested EIP-1271 contract signatures for members who
// are another user's primary wallet, direct EOA signatures for members who
// are a plain external EOA - see resolveGroupOwnerSigner), and submits it
// through a pool relayer (internal/relayer), held until the submission
// confirms on-chain (PLAN.md §13.12). What's still deliberately missing:
// per-Safe nonce reservation (a proposal's SafeNonce is a naive on-chain
// read at proposal time, not an atomically reserved one - PLAN.md §13.12
// risk 1) and the stale-action expiry sweep, both Phase 6's job.
package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/sharedaccess/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
	"wallet-backend/internal/relayer"
	"wallet-backend/internal/safe"
	"wallet-backend/internal/validators"
)

// BlockchainClient is the slice of *network.Client this service needs -
// narrowed to an interface so tests can exercise the approval-threshold
// execution path with a fake instead of a live Base RPC connection.
type BlockchainClient interface {
	SignAndSubmitTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
	// WaitForReceipt blocks until a submitted execution transaction
	// confirms (or ctx is done), reporting whether it succeeded - see
	// network.Client.WaitForReceipt.
	WaitForReceipt(ctx context.Context, txHash string) (bool, error)
	// SafeNonce reads a Safe's current on-chain nonce - see
	// safe.EncodeNonceCalldata's doc comment for why and the concurrency
	// caveat.
	SafeNonce(ctx context.Context, safeAddress string) (*big.Int, error)
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
	// ChainID identifies the chain a group's Safe is deployed on, needed
	// to compute its EIP-712 domain separator (safe.DomainSeparator) when
	// hashing or verifying a SafeTx - see PLAN.md §8.
	ChainID *big.Int
	// RelayerPool is the pool of backend-operated EOAs that submit every
	// real execTransaction call (PLAN.md §13.10 Phase 4/§13.12) - never
	// the group's own funds and never a single dedicated key, so many
	// concurrent executions across many different groups don't contend
	// for one account's Ethereum nonce.
	RelayerPool *relayer.Pool
}

func New(db *gorm.DB, blockchain BlockchainClient, deployerKeySalt string, chainID int64, relayerPool *relayer.Pool) *Service {
	return &Service{
		DB:              db,
		Blockchain:      blockchain,
		DeployerKeySalt: deployerKeySalt,
		ChainID:         big.NewInt(chainID),
		RelayerPool:     relayerPool,
	}
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

// resolveGroupOwnerSigner reports, for a Safe owner address (any member
// with CanApprove(role)), whether it's a registered user's primary wallet
// - in which case the actual EOA that must sign is that user's current
// SignerAddress, and the signature has to be nested through EIP-1271
// (PLAN.md §13.4) since the wallet itself is a Safe, not a key - or a
// plain external EOA acting as a direct Safe owner, in which case it signs
// (and is verified) directly. validateSafeOwnerCandidate already
// guarantees a nested case's primary wallet is deployed, so it's always
// safe to treat it as reachable via EIP-1271.
func (s *Service) resolveGroupOwnerSigner(memberAddress string) (nested bool, signerAddress string, err error) {
	var user usersModels.User
	dbErr := s.DB.Where("LOWER(address) = LOWER(?)", memberAddress).First(&user).Error
	if dbErr == nil {
		return true, user.SignerAddress, nil
	}
	if !errors.Is(dbErr, gorm.ErrRecordNotFound) {
		return false, "", apperrors.Internal("failed to resolve group member's signer")
	}
	return false, memberAddress, nil
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
func (s *Service) ProposePayment(ctx context.Context, proposerAddress string, groupID uint, description, recipient, tokenAddress, amount string) (*models.PendingAction, error) {
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

	return s.createPendingAction(ctx, group, proposerAddress, models.ActionPayment, description, to, tokenAddr, value, data)
}

// ProposeContractCall proposes an arbitrary contract call (e.g. a swap
// router call built the same way internal/components/swaps builds one) from
// the group's wallet. Only an INITIATOR may propose.
func (s *Service) ProposeContractCall(ctx context.Context, proposerAddress string, groupID uint, kind models.ActionKind, description, contractAddress, valueWei, dataHex string) (*models.PendingAction, error) {
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

	return s.createPendingAction(ctx, group, proposerAddress, kind, description, contractAddress, "", valueWei, dataHex)
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

func (s *Service) createPendingAction(ctx context.Context, group *models.ClosedGroup, proposer string, kind models.ActionKind, description, to, tokenAddress, value, data string) (*models.PendingAction, error) {
	if group.Address == nil {
		return nil, apperrors.Internal("group has no on-chain wallet address")
	}
	nonce, err := s.Blockchain.SafeNonce(ctx, *group.Address)
	if err != nil {
		return nil, apperrors.Internal("failed to read the group wallet's on-chain nonce: " + err.Error())
	}

	action := models.PendingAction{
		GroupID:           group.ID,
		ProposerAddress:   proposer,
		Kind:              kind,
		Description:       description,
		To:                to,
		TokenAddress:      tokenAddress,
		Value:             value,
		Data:              data,
		SafeNonce:         nonce.String(),
		RequiredApprovals: group.Threshold,
		Status:            models.ActionPending,
	}
	if err := s.DB.Create(&action).Error; err != nil {
		return nil, apperrors.Internal("failed to propose action")
	}
	return &action, nil
}

// buildSafeTx reconstructs the safe.SafeTx an action's approvals were - or
// must be - signed over: a plain CALL (this codebase never proposes a
// delegatecall) to action.To with action.Value/Data, at the nonce fixed at
// proposal time (action.SafeNonce). SafeTxGas/BaseGas/GasPrice/GasToken/
// RefundReceiver are always zero - see safe.EncodeExecTransactionCalldata's
// doc comment for why (the relayer pool pays gas directly rather than
// asking the Safe for an in-band refund).
func buildSafeTx(action *models.PendingAction) (safe.SafeTx, error) {
	value, ok := new(big.Int).SetString(action.Value, 10)
	if !ok {
		return safe.SafeTx{}, apperrors.Internal("stored action value is not a valid integer")
	}
	nonce, ok := new(big.Int).SetString(action.SafeNonce, 10)
	if !ok {
		return safe.SafeTx{}, apperrors.Internal("stored action safe nonce is not a valid integer")
	}
	var data []byte
	if action.Data != "" && action.Data != "0x" {
		data = common.FromHex(action.Data)
	}
	return safe.SafeTx{
		To:        common.HexToAddress(action.To),
		Value:     value,
		Data:      data,
		Operation: safe.OperationCall,
		Nonce:     nonce,
	}, nil
}

// digestToSign computes the exact 32-byte digest memberAddress's approval
// must be a personal_sign signature of: the plain SafeTxHash for a direct
// EOA owner, or, for a member that's a registered user's primary wallet
// (nested EIP-1271, PLAN.md §13.4), the nested MessageHashForSafe that
// primary wallet's own owner (its current signer) must sign instead - see
// resolveGroupOwnerSigner.
func (s *Service) digestToSign(groupAddress common.Address, tx safe.SafeTx, memberAddress string) (common.Hash, error) {
	domainSeparator := safe.DomainSeparator(s.ChainID, groupAddress)
	nested, _, err := s.resolveGroupOwnerSigner(memberAddress)
	if err != nil {
		return common.Hash{}, err
	}
	if !nested {
		return safe.SafeTxHash(domainSeparator, tx), nil
	}
	outerPreImage := safe.EncodeTransactionData(domainSeparator, tx)
	memberDomainSeparator := safe.DomainSeparator(s.ChainID, common.HexToAddress(memberAddress))
	return safe.MessageHashForSafe(memberDomainSeparator, outerPreImage), nil
}

// DigestToSign returns the 0x-prefixed hex digest memberAddress must
// personal_sign to approve actionID - the real on-chain SafeTxHash (or its
// nested EIP-1271 wrapping), replacing what used to be a purely off-chain
// descriptive message once execution became real (PLAN.md §13.10 Phase 4).
func (s *Service) DigestToSign(actionID uint, memberAddress string) (string, error) {
	action, err := s.GetAction(actionID)
	if err != nil {
		return "", err
	}
	group, err := s.GetGroup(action.GroupID)
	if err != nil {
		return "", err
	}
	if group.Address == nil {
		return "", apperrors.Internal("group has no on-chain wallet address")
	}
	tx, err := buildSafeTx(action)
	if err != nil {
		return "", err
	}
	digest, err := s.digestToSign(common.HexToAddress(*group.Address), tx, memberAddress)
	if err != nil {
		return "", err
	}
	return digest.Hex(), nil
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

// ApproveAction records memberAddress's signed approval of actionID -
// verified as a personal_sign signature over the real digest DigestToSign
// reports for that member (PLAN.md §13.10 Phase 4) - and, if that brings
// the tally to the group's threshold, executes the action for real.
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
		// the threshold was already met (e.g. a transient RPC error or an
		// on-chain revert) - retry execution rather than rejecting this as a
		// duplicate, since without this every approver having already signed
		// once would permanently strand the action with no way to move it
		// forward.
		return s.tallyAndMaybeExecute(ctx, action)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for an existing approval")
	}

	group, err := s.GetGroup(action.GroupID)
	if err != nil {
		return nil, err
	}
	if group.Address == nil {
		return nil, apperrors.Internal("group has no on-chain wallet address")
	}
	tx, err := buildSafeTx(action)
	if err != nil {
		return nil, err
	}
	digest, err := s.digestToSign(common.HexToAddress(*group.Address), tx, memberAddress)
	if err != nil {
		return nil, err
	}
	_, signerAddress, err := s.resolveGroupOwnerSigner(memberAddress)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(signatureHex, "0x") {
		return nil, apperrors.BadRequest("signature must be 0x-prefixed hex")
	}
	sigBytes := common.FromHex(signatureHex)
	valid, err := cryptoutil.VerifyPersonalSignBytes(digest.Bytes(), sigBytes, common.HexToAddress(signerAddress))
	if err != nil {
		return nil, apperrors.BadRequest("invalid signature: " + err.Error())
	}
	if !valid {
		return nil, apperrors.Unauthorized("signature does not match the approving member's current signing key")
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

// buildPackedSignature turns one recorded off-chain approval into the
// safe.Signature PackSignatures expects: a direct EOA signature for a
// plain external-EOA owner, or a nested EIP-1271 contract signature - the
// inner PackSignatures blob over that primary wallet's own (single) owner
// - for a member that's a registered user's primary wallet (PLAN.md
// §13.4).
func (s *Service) buildPackedSignature(domainSeparator common.Hash, tx safe.SafeTx, approval models.PendingActionApproval) (safe.Signature, error) {
	nested, signerAddress, err := s.resolveGroupOwnerSigner(approval.MemberAddress)
	if err != nil {
		return safe.Signature{}, err
	}
	rawSig := common.FromHex(approval.Signature)
	if !nested {
		return safe.EOAPersonalSignSignature(common.HexToAddress(approval.MemberAddress), rawSig)
	}

	innerSig, err := safe.EOAPersonalSignSignature(common.HexToAddress(signerAddress), rawSig)
	if err != nil {
		return safe.Signature{}, err
	}
	innerPacked, err := safe.PackSignatures([]safe.Signature{innerSig})
	if err != nil {
		return safe.Signature{}, err
	}
	return safe.ContractSignature(common.HexToAddress(approval.MemberAddress), innerPacked), nil
}

// executeAction is called once an action has reached its approval
// threshold: it packs every recorded approval into the real signatures
// blob Safe.execTransaction expects, claims a relayer, submits the call,
// and waits for it to confirm before releasing the relayer and marking
// the action EXECUTED (PLAN.md §13.10 Phase 4/§13.12). A submission or
// on-chain failure reverts the action to PENDING so the next approval call
// (via ApproveAction's already-approved retry branch) tries again - Phase
// 4 has no other terminal state to leave a failed attempt in, and a fresh
// attempt is safe to retry since nothing about a failed execTransaction
// call changes the Safe's own nonce.
func (s *Service) executeAction(ctx context.Context, action *models.PendingAction) (*models.PendingAction, error) {
	group, err := s.GetGroup(action.GroupID)
	if err != nil {
		return nil, err
	}
	if group.Address == nil {
		return nil, apperrors.Internal("group has no on-chain wallet address")
	}
	groupAddr := common.HexToAddress(*group.Address)

	tx, err := buildSafeTx(action)
	if err != nil {
		return nil, err
	}
	domainSeparator := safe.DomainSeparator(s.ChainID, groupAddr)

	var approvals []models.PendingActionApproval
	if err := s.DB.Where("pending_action_id = ?", action.ID).Find(&approvals).Error; err != nil {
		return nil, apperrors.Internal("failed to load recorded approvals")
	}
	sigs := make([]safe.Signature, 0, len(approvals))
	for _, approval := range approvals {
		sig, err := s.buildPackedSignature(domainSeparator, tx, approval)
		if err != nil {
			return nil, err
		}
		sigs = append(sigs, sig)
	}
	packed, err := safe.PackSignatures(sigs)
	if err != nil {
		return nil, apperrors.Internal("failed to pack approval signatures: " + err.Error())
	}
	calldata, err := safe.EncodeExecTransactionCalldata(tx, packed)
	if err != nil {
		return nil, apperrors.Internal("failed to encode execution calldata")
	}

	relayerKey, err := s.RelayerPool.Claim(ctx)
	if err != nil {
		return nil, apperrors.Internal("no relayer currently available: " + err.Error())
	}
	relayerAddr := crypto.PubkeyToAddress(relayerKey.PublicKey)

	txHash, err := s.Blockchain.SignAndSubmitTx(ctx, relayerKey, &groupAddr, big.NewInt(0), calldata, nil)
	if err != nil {
		s.RelayerPool.Release(relayerAddr)
		return nil, apperrors.Internal("failed to submit execution transaction: " + err.Error())
	}

	action.Status = models.ActionSubmitted
	action.TxHash = txHash
	action.RelayerAddress = relayerAddr.Hex()
	if err := s.DB.Save(action).Error; err != nil {
		log.Printf("[sharedaccess] failed to persist SUBMITTED status for action %d (tx already broadcast: %s): %v", action.ID, txHash, err)
	}

	return s.awaitExecution(ctx, action, relayerAddr, txHash)
}

// awaitExecution blocks until txHash confirms (or ctx is done), releases
// relayerAddr back to the pool exactly then - per PLAN.md §13.12, not at
// the instant of submission - and finalizes action's status.
func (s *Service) awaitExecution(ctx context.Context, action *models.PendingAction, relayerAddr common.Address, txHash string) (*models.PendingAction, error) {
	defer s.RelayerPool.Release(relayerAddr)

	success, err := s.Blockchain.WaitForReceipt(ctx, txHash)
	if err != nil || !success {
		action.Status = models.ActionPending
		if saveErr := s.DB.Save(action).Error; saveErr != nil {
			log.Printf("[sharedaccess] failed to revert action %d to PENDING after a failed execution: %v", action.ID, saveErr)
		}
		if err != nil {
			return nil, apperrors.Internal("execution transaction did not confirm: " + err.Error())
		}
		return nil, apperrors.Internal("execution transaction reverted on-chain")
	}

	action.Status = models.ActionExecuted
	if err := s.DB.Save(action).Error; err != nil {
		return nil, apperrors.Internal("execution succeeded on-chain but failed to record locally: " + err.Error())
	}
	return action, nil
}

// ReconcileRelayers re-marks in-use any relayer whose last known
// submission (per the database) was still SUBMITTED - never resolved -
// when the process last stopped, then resumes waiting on each one. This
// is the direct counterpart to the original's own startup-reconciliation
// goroutine (PLAN.md §13.12): the in-memory relayer pool has no memory of
// its own across a restart, so without this a relayer whose last
// submission's outcome is still unknown could be handed out for new work
// immediately. Call once at boot, before serving traffic.
func (s *Service) ReconcileRelayers(ctx context.Context) {
	var stuck []models.PendingAction
	if err := s.DB.Where("status = ?", models.ActionSubmitted).Find(&stuck).Error; err != nil {
		log.Printf("[sharedaccess] failed to scan for in-flight actions at startup: %v", err)
		return
	}
	if len(stuck) == 0 {
		return
	}

	var reserve []common.Address
	for _, a := range stuck {
		if a.RelayerAddress != "" {
			reserve = append(reserve, common.HexToAddress(a.RelayerAddress))
		}
	}
	s.RelayerPool.ReserveAtStartup(reserve)

	for i := range stuck {
		action := stuck[i]
		if action.RelayerAddress == "" || action.TxHash == "" {
			// Nothing to wait on - revert immediately so the next approval
			// retries execution from scratch.
			action.Status = models.ActionPending
			if err := s.DB.Save(&action).Error; err != nil {
				log.Printf("[sharedaccess] failed to revert incomplete action %d to PENDING at startup: %v", action.ID, err)
			}
			continue
		}
		go func(action models.PendingAction) {
			if _, err := s.awaitExecution(ctx, &action, common.HexToAddress(action.RelayerAddress), action.TxHash); err != nil {
				log.Printf("[sharedaccess] startup reconciliation for action %d: %v", action.ID, err)
			}
		}(action)
	}
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
