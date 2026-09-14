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
// confirms on-chain (PLAN.md §13.12). Phase 6 (reserveSafeNonce) makes a
// proposal's SafeTxHash-fixing nonce read atomic - one Safe may have at
// most one nonce-consuming action outstanding at a time, enforced under a
// row lock - and adds ExpireStalePendingActions, the stale-action sweep
// PLAN.md §13.12 flags as missing relative to the original's own
// fiat-invoice expiry.
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
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	// SafeOwners reads a Safe's current owners - needed (PLAN.md §13.10
	// Phase 5) to compute removeOwner's prevOwner argument.
	SafeOwners(ctx context.Context, safeAddress string) ([]common.Address, error)
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

	// domainHooks holds every registered DomainHook, keyed by domain - see
	// RegisterDomainHook.
	domainHooks map[string]DomainHook
}

func New(db *gorm.DB, blockchain BlockchainClient, deployerKeySalt string, chainID int64, relayerPool *relayer.Pool) *Service {
	return &Service{
		DB:              db,
		Blockchain:      blockchain,
		DeployerKeySalt: deployerKeySalt,
		ChainID:         big.NewInt(chainID),
		RelayerPool:     relayerPool,
		domainHooks:     map[string]DomainHook{},
	}
}

// DomainHook is called once a domain-tagged PendingAction (one proposed
// via ProposePayment/ProposeContractCall with a non-empty domain) reaches
// a terminal state - EXECUTED or REJECTED (including via
// ExpireStalePendingActions' expiry sweep) - so the business component
// that proposed it (crypto withdrawals, tokenization, market-making, ...)
// can react: crediting or reverting its own domain record, releasing an
// escrow hold, notifying a user, etc. PLAN.md §13.8 flags this as the
// piece needed to keep sharedaccess itself domain-agnostic - it never
// creates a domain record or knows what one means, only carries
// Domain/RelatedRecordID back to whoever registered a hook for that
// domain.
type DomainHook func(action *models.PendingAction)

// RegisterDomainHook wires domain's post-execution hook - see DomainHook's
// doc comment. Call once per domain at boot (main.go), after both
// components exist - mirroring the same post-construction callback-wiring
// pattern this codebase already uses for kyc.OnBVNVerified and
// tokenization.CreateFiatInvoice, so a business component's own package
// never has to import sharedaccess/services directly (nor sharedaccess
// import any of them). Panics on a second registration for the same
// domain - silently keeping only the first (or the last) would hide a
// real wiring bug at boot rather than surfacing it immediately.
func (s *Service) RegisterDomainHook(domain string, hook DomainHook) {
	if _, exists := s.domainHooks[domain]; exists {
		panic("sharedaccess: duplicate domain hook registration for domain " + domain)
	}
	s.domainHooks[domain] = hook
}

// invokeDomainHook calls action's registered domain hook, if any - a
// no-op for the (overwhelmingly common) case of an ordinary,
// non-domain-tagged action. Logs rather than fails if a domain was set
// but nothing ever registered a hook for it: the action itself already
// reached its terminal state correctly by this point, and a missing hook
// is a business-component wiring bug this package has no way to recover
// from on its behalf.
func (s *Service) invokeDomainHook(action *models.PendingAction) {
	if action.Domain == "" {
		return
	}
	hook, ok := s.domainHooks[action.Domain]
	if !ok {
		log.Printf("[sharedaccess] action %d finished with domain %q but no hook is registered for it", action.ID, action.Domain)
		return
	}
	hook(action)
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
// wallet. Only an INITIATOR may propose. domain/relatedRecordID
// (PLAN.md §13.10 Phase 7) tag this action as belonging to another
// business component's own record - pass "", "" for an ordinary,
// directly-user-proposed payment; see models.PendingAction.Domain and
// DomainHook.
func (s *Service) ProposePayment(ctx context.Context, proposerAddress string, groupID uint, description, recipient, tokenAddress, amount, domain, relatedRecordID string) (*models.PendingAction, error) {
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

	return s.createPendingAction(ctx, group, proposerAddress, pendingActionParams{
		kind: models.ActionPayment, description: description,
		to: to, tokenAddress: tokenAddr, value: value, data: data,
		domain: domain, relatedRecordID: relatedRecordID,
	})
}

// ProposeContractCall proposes an arbitrary contract call (e.g. a swap
// router call built the same way internal/components/swaps builds one, or
// a tokenization mint call) from the group's wallet. Only an INITIATOR may
// propose. domain/relatedRecordID - see ProposePayment's doc comment.
func (s *Service) ProposeContractCall(ctx context.Context, proposerAddress string, groupID uint, kind models.ActionKind, description, contractAddress, valueWei, dataHex, domain, relatedRecordID string) (*models.PendingAction, error) {
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

	return s.createPendingAction(ctx, group, proposerAddress, pendingActionParams{
		kind: kind, description: description,
		to: contractAddress, value: valueWei, data: dataHex,
		domain: domain, relatedRecordID: relatedRecordID,
	})
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

// pendingActionParams is everything createPendingAction needs beyond the
// group and proposer - a struct rather than a long positional parameter
// list now that Phase 5's group-management kinds add three more
// (target-member/role/threshold) fields most callers never set.
type pendingActionParams struct {
	kind         models.ActionKind
	description  string
	to           string // "" for a pure application-level change with no Safe call at all (PLAN.md §13.10 Phase 5)
	tokenAddress string
	value        string
	data         string

	// targetMemberAddress/targetRole/newThreshold are set only by the
	// group-management Propose* functions below.
	targetMemberAddress string
	targetRole          models.GroupRole
	newThreshold        int

	// domain/relatedRecordID (PLAN.md §13.10 Phase 7) are set only when
	// ProposePayment/ProposeContractCall is called on behalf of another
	// business component rather than directly by an end user - see
	// models.PendingAction.Domain's doc comment and DomainHook.
	domain          string
	relatedRecordID string
}

func (s *Service) createPendingAction(ctx context.Context, group *models.ClosedGroup, proposer string, p pendingActionParams) (*models.PendingAction, error) {
	if group.Address == nil {
		return nil, apperrors.Internal("group has no on-chain wallet address")
	}
	action := models.PendingAction{
		GroupID:             group.ID,
		ProposerAddress:     proposer,
		Kind:                p.kind,
		Description:         p.description,
		To:                  p.to,
		TokenAddress:        p.tokenAddress,
		Value:               p.value,
		Data:                p.data,
		TargetMemberAddress: p.targetMemberAddress,
		TargetRole:          p.targetRole,
		NewThreshold:        p.newThreshold,
		Domain:              p.domain,
		RelatedRecordID:     p.relatedRecordID,
		RequiredApprovals:   group.Threshold,
		Status:              models.ActionPending,
	}
	// A pure application-level change (an added/removed VIEW_ONLY/
	// INITIATOR member, or a group disable) never submits anything
	// on-chain, so there's no SafeTxHash - and hence no nonce - to
	// reserve for it at all.
	if p.to == "" {
		if err := s.DB.Create(&action).Error; err != nil {
			return nil, apperrors.Internal("failed to propose action")
		}
		return &action, nil
	}

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		nonce, rerr := s.reserveSafeNonce(ctx, tx, group)
		if rerr != nil {
			return rerr
		}
		action.SafeNonce = nonce.String()
		if cerr := tx.Create(&action).Error; cerr != nil {
			return apperrors.Internal("failed to propose action")
		}
		return nil
	})
	if err != nil {
		if appErr, ok := err.(*apperrors.AppError); ok {
			return nil, appErr
		}
		return nil, apperrors.Internal("failed to propose action: " + err.Error())
	}
	return &action, nil
}

// reserveSafeNonce fixes the Safe nonce a new nonce-consuming action's
// SafeTxHash will be computed against (PLAN.md §13.10 Phase 6/§13.12 risk
// 1), called inside the same DB transaction that then inserts the action.
//
// A Safe's own nonce only ever increments on a *successful* on-chain
// execTransaction call - never on this application's own REJECTED status,
// which has no on-chain effect at all. That rules out the simpler scheme
// of "assign onChainNonce + count(non-terminal actions)" verbatim: if an
// earlier-reserved action is rejected before executing, its nonce slot is
// never actually consumed on the real Safe, so a later action holding the
// next nonce up would revert forever once it tried to execute, with no
// way to recover short of renumbering every other outstanding action
// (invalidating every signature already collected for them in the
// process). Rather than build that renumbering machinery, this enforces
// the simpler, provably-correct invariant a Safe's own nonce sequence
// already implies: at most one nonce-consuming action may be
// PENDING/SUBMITTED per group at a time. Locking the ClosedGroup row for
// the duration of this check-then-insert is what makes two concurrent
// proposals against the same group race safely instead of both reading
// "none outstanding" and colliding - the second blocks on the lock until
// the first commits, then correctly sees the first's action and is
// rejected.
func (s *Service) reserveSafeNonce(ctx context.Context, tx *gorm.DB, group *models.ClosedGroup) (*big.Int, error) {
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&models.ClosedGroup{}, group.ID).Error; err != nil {
		return nil, apperrors.Internal("failed to lock group for nonce reservation: " + err.Error())
	}

	var outstanding int64
	err := tx.Model(&models.PendingAction{}).
		Where(`group_id = ? AND "to" != '' AND status IN ?`, group.ID, []models.ActionStatus{models.ActionPending, models.ActionSubmitted}).
		Count(&outstanding).Error
	if err != nil {
		return nil, apperrors.Internal("failed to check for an outstanding on-chain action")
	}
	if outstanding > 0 {
		return nil, apperrors.Conflict("this wallet already has an on-chain action awaiting approval or execution - resolve it before proposing another")
	}

	nonce, err := s.Blockchain.SafeNonce(ctx, *group.Address)
	if err != nil {
		return nil, apperrors.Internal("failed to read the group wallet's on-chain nonce: " + err.Error())
	}
	return nonce, nil
}

// managementActionKinds are the four group-management kinds Phase 5 adds -
// see their doc comments in the models package.
var managementActionKinds = []models.ActionKind{
	models.ActionAddMember, models.ActionRemoveMember, models.ActionChangeThreshold, models.ActionDisableGroup,
}

// requireNoConflictingManagementAction blocks proposing a new group-
// management action while another one is still in flight on the same
// group - mirroring the original's own rule for DISABLE SHARED ACCESS
// ("blocked while another shared-access change is already pending"),
// generalized to every management kind: two concurrent owner-set changes
// racing against the same Safe is exactly the kind of conflict PLAN.md
// §13.12 flags elsewhere, and there's no reason to allow it here either.
func (s *Service) requireNoConflictingManagementAction(groupID uint) error {
	var count int64
	err := s.DB.Model(&models.PendingAction{}).
		Where("group_id = ? AND kind IN ? AND status IN ?", groupID, managementActionKinds, []models.ActionStatus{models.ActionPending, models.ActionSubmitted}).
		Count(&count).Error
	if err != nil {
		return apperrors.Internal("failed to check for a conflicting membership change")
	}
	if count > 0 {
		return apperrors.Conflict("another membership change is already pending or in flight on this group")
	}
	return nil
}

// ProposeAddMember proposes adding newMemberAddress to the group with
// role, with the group's threshold becoming newThreshold once executed
// (Safe.addOwnerWithThreshold and this application's own GroupMember row
// change together, atomically from the caller's point of view - see
// applyMembershipSideEffect). If role can approve (models.CanApprove),
// newMemberAddress must pass validateSafeOwnerCandidate exactly like
// CreateGroup requires, and the proposal becomes a real Safe self-call;
// otherwise (VIEW_ONLY or a plain INITIATOR) it never touches the Safe's
// owner set at all, so newThreshold must simply equal the group's current
// threshold, kept explicit rather than silently ignored so a caller is
// never surprised by which of the two paths ran.
func (s *Service) ProposeAddMember(ctx context.Context, proposerAddress string, groupID uint, newMemberAddress string, role models.GroupRole, newThreshold int) (*models.PendingAction, error) {
	group, err := s.requireInitiator(groupID, proposerAddress)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoConflictingManagementAction(groupID); err != nil {
		return nil, err
	}
	if !validators.IsValidAddress(newMemberAddress) {
		return nil, apperrors.BadRequest("invalid member address")
	}
	switch role {
	case models.RoleInitiator, models.RoleApprover, models.RoleViewOnly, models.RoleInitiatorApprover:
	default:
		return nil, apperrors.BadRequest("invalid role")
	}
	if newThreshold < 1 {
		return nil, apperrors.BadRequest("threshold must be at least 1")
	}
	var existing models.GroupMember
	err = s.DB.Where("group_id = ? AND member_address = ?", groupID, newMemberAddress).First(&existing).Error
	if err == nil {
		return nil, apperrors.Conflict(newMemberAddress + " is already a member of this group")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check existing membership")
	}

	var to, data string
	if models.CanApprove(role) {
		if err := s.validateSafeOwnerCandidate(newMemberAddress); err != nil {
			return nil, err
		}
		calldata, err := safe.EncodeAddOwnerWithThresholdCalldata(common.HexToAddress(newMemberAddress), big.NewInt(int64(newThreshold)))
		if err != nil {
			return nil, apperrors.Internal("failed to encode addOwnerWithThreshold calldata")
		}
		to, data = *group.Address, "0x"+common.Bytes2Hex(calldata)
	} else if newThreshold != group.Threshold {
		return nil, apperrors.BadRequest("newThreshold must equal the group's current threshold when adding a non-approving member")
	}

	return s.createPendingAction(ctx, group, proposerAddress, pendingActionParams{
		kind: models.ActionAddMember, description: fmt.Sprintf("add %s as %s", newMemberAddress, role),
		to: to, value: "0", data: data,
		targetMemberAddress: newMemberAddress, targetRole: role, newThreshold: newThreshold,
	})
}

// ProposeRemoveMember proposes removing memberAddress from the group,
// with the group's threshold becoming newThreshold once executed. If the
// member currently holds an approve-capable role, the proposal becomes a
// real Safe.removeOwner self-call (its prevOwner argument computed from
// the Safe's own current owner order - see safe.FindPrevOwner); otherwise
// it's a pure application-level removal and newThreshold must equal the
// group's current threshold.
func (s *Service) ProposeRemoveMember(ctx context.Context, proposerAddress string, groupID uint, memberAddress string, newThreshold int) (*models.PendingAction, error) {
	group, err := s.requireInitiator(groupID, proposerAddress)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoConflictingManagementAction(groupID); err != nil {
		return nil, err
	}
	if newThreshold < 1 {
		return nil, apperrors.BadRequest("threshold must be at least 1")
	}
	var member models.GroupMember
	err = s.DB.Where("group_id = ? AND member_address = ?", groupID, memberAddress).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound(memberAddress + " is not a member of this group")
		}
		return nil, apperrors.Internal("failed to load group membership")
	}

	var to, data string
	if models.CanApprove(member.Role) {
		owners, err := s.Blockchain.SafeOwners(ctx, *group.Address)
		if err != nil {
			return nil, apperrors.Internal("failed to read the group wallet's current owners: " + err.Error())
		}
		target := common.HexToAddress(memberAddress)
		prevOwner, err := safe.FindPrevOwner(owners, target)
		if err != nil {
			return nil, apperrors.Conflict("member is not currently a Safe owner on-chain: " + err.Error())
		}
		remainingApprovers := 0
		for _, o := range owners {
			if o != target {
				remainingApprovers++
			}
		}
		if newThreshold > remainingApprovers {
			return nil, apperrors.BadRequest(fmt.Sprintf("threshold (%d) would exceed the number of approvers remaining after removal (%d)", newThreshold, remainingApprovers))
		}
		calldata, err := safe.EncodeRemoveOwnerCalldata(prevOwner, target, big.NewInt(int64(newThreshold)))
		if err != nil {
			return nil, apperrors.Internal("failed to encode removeOwner calldata")
		}
		to, data = *group.Address, "0x"+common.Bytes2Hex(calldata)
	} else if newThreshold != group.Threshold {
		return nil, apperrors.BadRequest("newThreshold must equal the group's current threshold when removing a non-approving member")
	}

	return s.createPendingAction(ctx, group, proposerAddress, pendingActionParams{
		kind: models.ActionRemoveMember, description: "remove " + memberAddress,
		to: to, value: "0", data: data,
		targetMemberAddress: memberAddress, newThreshold: newThreshold,
	})
}

// ProposeChangeThreshold proposes Safe.changeThreshold(newThreshold) with
// no other change to the owner set.
func (s *Service) ProposeChangeThreshold(ctx context.Context, proposerAddress string, groupID uint, newThreshold int) (*models.PendingAction, error) {
	group, err := s.requireInitiator(groupID, proposerAddress)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoConflictingManagementAction(groupID); err != nil {
		return nil, err
	}
	if newThreshold < 1 {
		return nil, apperrors.BadRequest("threshold must be at least 1")
	}
	var approverCount int64
	err = s.DB.Model(&models.GroupMember{}).
		Where("group_id = ? AND role IN ?", groupID, []models.GroupRole{models.RoleApprover, models.RoleInitiatorApprover}).
		Count(&approverCount).Error
	if err != nil {
		return nil, apperrors.Internal("failed to count current approvers")
	}
	if int64(newThreshold) > approverCount {
		return nil, apperrors.BadRequest(fmt.Sprintf("threshold (%d) exceeds the number of approvers (%d)", newThreshold, approverCount))
	}

	calldata, err := safe.EncodeChangeThresholdCalldata(big.NewInt(int64(newThreshold)))
	if err != nil {
		return nil, apperrors.Internal("failed to encode changeThreshold calldata")
	}
	return s.createPendingAction(ctx, group, proposerAddress, pendingActionParams{
		kind: models.ActionChangeThreshold, description: fmt.Sprintf("change threshold to %d", newThreshold),
		to: *group.Address, value: "0", data: "0x" + common.Bytes2Hex(calldata),
		newThreshold: newThreshold,
	})
}

// ProposeDisableGroup proposes disabling the group - the equivalent of the
// original's DELETE /v1/shared-access/users/account. A pure application-
// level flag, never a Safe call (see models.ActionDisableGroup's doc
// comment) - once executed, no further action may be proposed against
// this group (requireInitiator already checks ClosedGroup.Disabled).
func (s *Service) ProposeDisableGroup(ctx context.Context, proposerAddress string, groupID uint) (*models.PendingAction, error) {
	group, err := s.requireInitiator(groupID, proposerAddress)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoConflictingManagementAction(groupID); err != nil {
		return nil, err
	}
	return s.createPendingAction(ctx, group, proposerAddress, pendingActionParams{
		kind: models.ActionDisableGroup, description: "disable shared access", value: "0",
	})
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

// canonicalManagementDigest stands in for a real SafeTxHash on a
// group-management action that never calls the Safe at all (action.To ==
// "" - PLAN.md §13.10 Phase 5, e.g. adding/removing a VIEW_ONLY member, or
// disabling a group): there is no genuine on-chain transaction for
// approvers to commit to, but every approval should still be a concrete
// cryptographic commitment to exactly what's being approved rather than a
// bare "yes" - so approvers personal_sign this deterministic description
// instead.
func canonicalManagementDigest(action *models.PendingAction) common.Hash {
	return crypto.Keccak256Hash([]byte(fmt.Sprintf(
		"wallet-backend group-management action #%d\ngroup: %d\nkind: %s\ntargetMember: %s\ntargetRole: %s\nnewThreshold: %d",
		action.ID, action.GroupID, action.Kind, action.TargetMemberAddress, action.TargetRole, action.NewThreshold,
	)))
}

// digestToSign computes the exact 32-byte digest memberAddress's approval
// must be a personal_sign signature of. For an action that calls the Safe
// (action.To != ""): the plain SafeTxHash for a direct EOA owner, or, for
// a member that's a registered user's primary wallet (nested EIP-1271,
// PLAN.md §13.4), the nested MessageHashForSafe that primary wallet's own
// owner (its current signer) must sign instead - see
// resolveGroupOwnerSigner. For a pure application-level action
// (action.To == ""), the same nested-or-direct wrapping applies to
// canonicalManagementDigest instead, since there's no real SafeTx to hash.
func (s *Service) digestToSign(groupAddress common.Address, action *models.PendingAction, memberAddress string) (common.Hash, error) {
	domainSeparator := safe.DomainSeparator(s.ChainID, groupAddress)
	nested, _, err := s.resolveGroupOwnerSigner(memberAddress)
	if err != nil {
		return common.Hash{}, err
	}

	if action.To == "" {
		base := canonicalManagementDigest(action)
		if !nested {
			return base, nil
		}
		memberDomainSeparator := safe.DomainSeparator(s.ChainID, common.HexToAddress(memberAddress))
		return safe.MessageHashForSafe(memberDomainSeparator, base.Bytes()), nil
	}

	tx, err := buildSafeTx(action)
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
// nested EIP-1271 wrapping) for an action that calls the Safe, or
// canonicalManagementDigest's wrapping for one that doesn't - replacing
// what used to be a purely off-chain descriptive message for every action
// once execution became real (PLAN.md §13.10 Phase 4).
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
	digest, err := s.digestToSign(common.HexToAddress(*group.Address), action, memberAddress)
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
	digest, err := s.digestToSign(common.HexToAddress(*group.Address), action, memberAddress)
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
// threshold. Most kinds pack every recorded approval into the real
// signatures blob Safe.execTransaction expects, claim a relayer, submit
// the call, and wait for it to confirm before releasing the relayer and
// marking the action EXECUTED (PLAN.md §13.10 Phase 4/§13.12). Two kinds
// never touch the chain at all: ActionDisableGroup (always) and a
// group-management action targeting a non-approving member (action.To ==
// "" - see pendingActionParams' doc comment) - both are pure
// application-level changes, applied directly. A submission or on-chain
// failure reverts the action to PENDING so the next approval call (via
// ApproveAction's already-approved retry branch) tries again - Phase 4
// has no other terminal state to leave a failed attempt in, and a fresh
// attempt is safe to retry since nothing about a failed execTransaction
// call changes the Safe's own nonce.
func (s *Service) executeAction(ctx context.Context, action *models.PendingAction) (*models.PendingAction, error) {
	if action.Kind == models.ActionDisableGroup {
		return s.executeDisableGroup(action)
	}
	if action.To == "" {
		return s.executeMembershipOnly(action)
	}

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

	result, err := s.awaitExecution(ctx, action, relayerAddr, txHash)
	if err != nil {
		return nil, err
	}
	if err := s.applyMembershipSideEffect(result); err != nil {
		// The Safe's own owner set (or threshold) really did change
		// on-chain at this point - surface the drift loudly rather than
		// silently leaving this application's GroupMember/ClosedGroup
		// rows out of sync with it.
		return nil, apperrors.Internal("execution succeeded on-chain but failed to sync local membership: " + err.Error())
	}
	return result, nil
}

// executeDisableGroup applies ActionDisableGroup's only effect - setting
// ClosedGroup.Disabled - and marks the action EXECUTED, both in one
// transaction. No Safe call, no relayer: see models.ActionDisableGroup's
// doc comment for why.
func (s *Service) executeDisableGroup(action *models.PendingAction) (*models.PendingAction, error) {
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.ClosedGroup{}).Where("id = ?", action.GroupID).Update("disabled", true).Error; err != nil {
			return err
		}
		action.Status = models.ActionExecuted
		return tx.Save(action).Error
	})
	if err != nil {
		return nil, apperrors.Internal("failed to disable group: " + err.Error())
	}
	s.invokeDomainHook(action)
	return action, nil
}

// executeMembershipOnly applies an add/remove-member action that never
// touched the Safe's own owner set (action.To == "") - a non-approving
// member's addition or removal, pure application-level bookkeeping.
func (s *Service) executeMembershipOnly(action *models.PendingAction) (*models.PendingAction, error) {
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := applyMembershipSideEffectTx(tx, action); err != nil {
			return err
		}
		action.Status = models.ActionExecuted
		return tx.Save(action).Error
	})
	if err != nil {
		return nil, apperrors.Internal("failed to apply membership change: " + err.Error())
	}
	s.invokeDomainHook(action)
	return action, nil
}

// applyMembershipSideEffect mirrors an on-chain-confirmed group-management
// action's effect into this application's own GroupMember/ClosedGroup
// rows - the counterpart, for the real-Safe-call kinds, to what
// executeMembershipOnly and executeDisableGroup already do for the two
// kinds that never touch the chain at all.
func (s *Service) applyMembershipSideEffect(action *models.PendingAction) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		return applyMembershipSideEffectTx(tx, action)
	})
}

// applyMembershipSideEffectTx is applyMembershipSideEffect's actual logic,
// factored out so executeMembershipOnly can apply it inside the same
// transaction that also marks the action EXECUTED.
func applyMembershipSideEffectTx(tx *gorm.DB, action *models.PendingAction) error {
	switch action.Kind {
	case models.ActionAddMember:
		if err := tx.Create(&models.GroupMember{GroupID: action.GroupID, MemberAddress: action.TargetMemberAddress, Role: action.TargetRole}).Error; err != nil {
			return err
		}
		return tx.Model(&models.ClosedGroup{}).Where("id = ?", action.GroupID).Update("threshold", action.NewThreshold).Error
	case models.ActionRemoveMember:
		if err := tx.Where("group_id = ? AND member_address = ?", action.GroupID, action.TargetMemberAddress).Delete(&models.GroupMember{}).Error; err != nil {
			return err
		}
		return tx.Model(&models.ClosedGroup{}).Where("id = ?", action.GroupID).Update("threshold", action.NewThreshold).Error
	case models.ActionChangeThreshold:
		return tx.Model(&models.ClosedGroup{}).Where("id = ?", action.GroupID).Update("threshold", action.NewThreshold).Error
	default:
		return nil
	}
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
	s.invokeDomainHook(action)
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
	s.invokeDomainHook(action)
	return action, nil
}

// ExpireStalePendingActions rejects every PENDING action older than ttl -
// the shared-access equivalent of the original's own
// ExpireStalePaymentInvoices, closing the gap PLAN.md §13.1's audit
// flagged: shared-access has always had a threshold but never a timeout,
// unlike every other pending-approval flow in this codebase. Only
// PENDING actions are in scope - a SUBMITTED action already has a relayer
// actively waiting on its confirmation (bounded by that call's own
// context, and recovered at the next restart by ReconcileRelayers if the
// process dies mid-wait), so it isn't "stale" in the sense this sweep
// exists to catch. Rejecting rather than deleting preserves the action's
// history exactly like any other rejection - and, for a nonce-consuming
// action, immediately frees its reservation for the next proposal, since
// reserveSafeNonce's outstanding-action check only ever counts
// PENDING/SUBMITTED rows. Rows are updated (and their domain hook, if
// any, invoked) one at a time rather than via a single bulk UPDATE -
// PLAN.md §13.10 Phase 7's DomainHook mechanism needs each individual
// action to report an expiry as a rejection to whichever business
// component proposed it, and expiry sweeps are infrequent and small
// enough in practice for this not to matter. Intended to run on a
// periodic ticker (main.go), mirroring the original's own 30-minute
// cadence for invoice expiry.
func (s *Service) ExpireStalePendingActions(ttl time.Duration) (int64, error) {
	cutoff := time.Now().Add(-ttl)
	var stale []models.PendingAction
	if err := s.DB.Where("status = ? AND created_at < ?", models.ActionPending, cutoff).Find(&stale).Error; err != nil {
		return 0, apperrors.Internal("failed to scan for stale pending actions: " + err.Error())
	}
	for i := range stale {
		stale[i].Status = models.ActionRejected
		stale[i].RejectionReason = "expired - no execution within the configured pending window"
		if err := s.DB.Save(&stale[i]).Error; err != nil {
			return int64(i), apperrors.Internal(fmt.Sprintf("failed to expire stale pending action %d: %v", stale[i].ID, err))
		}
		s.invokeDomainHook(&stale[i])
	}
	return int64(len(stale)), nil
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
