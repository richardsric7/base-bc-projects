package services

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/sharedaccess/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/relayer"
	"wallet-backend/internal/safe"
)

// fakeBlockchain is a BlockchainClient that never touches the network -
// SignAndSubmitTx just records every submission (CreateGroup's own Safe
// deployment and executeAction's real execTransaction call alike) and
// returns a canned hash; WaitForReceipt and SafeNonce are both
// configurable so tests can exercise both the happy path and the
// submission/confirmation failure paths executeAction is expected to
// recover from.
type fakeBlockchain struct {
	submitted []fakeSubmission
	hash      string
	err       error // if set, SignAndSubmitTx fails

	nonce      *big.Int // SafeNonce's return value; defaults to 0
	receiptOK  bool     // WaitForReceipt's success value; defaults to true (see newFakeBlockchain)
	receiptErr error    // if set, WaitForReceipt fails
	owners     []common.Address
	ownersErr  error // if set, SafeOwners fails
}

func newFakeBlockchain(hash string) *fakeBlockchain {
	return &fakeBlockchain{hash: hash, receiptOK: true}
}

type fakeSubmission struct {
	to   common.Address
	data []byte
}

func (f *fakeBlockchain) SignAndSubmitTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, _ *big.Int, data []byte, _ *uint64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.submitted = append(f.submitted, fakeSubmission{to: *to, data: append([]byte{}, data...)})
	return f.hash, nil
}

func (f *fakeBlockchain) WaitForReceipt(context.Context, string) (bool, error) {
	if f.receiptErr != nil {
		return false, f.receiptErr
	}
	return f.receiptOK, nil
}

func (f *fakeBlockchain) SafeNonce(context.Context, string) (*big.Int, error) {
	if f.nonce != nil {
		return f.nonce, nil
	}
	return big.NewInt(0), nil
}

func (f *fakeBlockchain) SafeOwners(context.Context, string) ([]common.Address, error) {
	if f.ownersErr != nil {
		return nil, f.ownersErr
	}
	return f.owners, nil
}

func (f *fakeBlockchain) NativeBalance(context.Context, string) (*big.Int, error) {
	return big.NewInt(1_000_000_000_000_000_000), nil
}

func (f *fakeBlockchain) ERC20BalanceOf(context.Context, string, string) (*big.Int, error) {
	return big.NewInt(500), nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(usersModels.Models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newTestService(t *testing.T, chain BlockchainClient) *Service {
	t.Helper()
	pool, err := relayer.NewPool("test-relayer-salt", 2)
	if err != nil {
		t.Fatalf("relayer.NewPool: %v", err)
	}
	return New(newTestDB(t), chain, "test-salt", 84532, pool)
}

func randomAddress(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), key
}

// signDigest personal_signs a 0x-prefixed 32-byte digest exactly the way
// cryptoutil.VerifyPersonalSignBytes verifies it (accounts.TextHash over
// the raw digest bytes, not their hex string) - matching what
// services.DigestToSign asks a real client to do.
func signDigest(t *testing.T, key *ecdsa.PrivateKey, digestHex string) string {
	t.Helper()
	digest := common.FromHex(digestHex)
	hash := accounts.TextHash(digest)
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return "0x" + common.Bytes2Hex(sig)
}

func TestCreateGroup_DeploysARealSafe(t *testing.T) {
	chain := newFakeBlockchain("0xdeployed")
	svc := newTestService(t, chain)
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "family wallet", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if group.Address == nil || *group.Address == "" {
		t.Fatal("expected a deployed group address")
	}
	if group.Purpose != models.PurposeWalletAccess {
		t.Fatalf("expected purpose WALLET_ACCESS, got %q", group.Purpose)
	}
	if len(chain.submitted) != 1 {
		t.Fatalf("expected exactly one deployment submission, got %d", len(chain.submitted))
	}
	if chain.submitted[0].to != safe.ProxyFactoryAddress {
		t.Fatalf("expected the deployment to target the SafeProxyFactory, got %s", chain.submitted[0].to.Hex())
	}

	other, err := svc.CreateGroup(context.Background(), "other wallet", 1, []MemberInput{
		{Address: approver, Role: models.RoleApprover},
		{Address: initiator, Role: models.RoleInitiator},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if *other.Address == *group.Address {
		t.Fatal("expected two groups with the same members to still deploy at different addresses - CreateGroup must randomize its saltNonce")
	}
}

func TestCreateGroup_ThresholdExceedsApprovers(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain(""))
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)

	_, err := svc.CreateGroup(context.Background(), "bad group", 2, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err == nil {
		t.Fatal("expected an error when threshold exceeds the number of approvers")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 400 {
		t.Fatalf("expected a 400 AppError, got %#v", err)
	}
}

func TestCreateGroup_CombinedRoleCountsAsAnApproverAndCanInitiate(t *testing.T) {
	chain := newFakeBlockchain("0xdeployed")
	svc := newTestService(t, chain)
	owner, ownerKey := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "sub-wallet", 1, []MemberInput{
		{Address: owner, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	action, err := svc.ProposePayment(context.Background(), owner, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment (as INITIATOR_APPROVER) returned error: %v", err)
	}
	digest, err := svc.DigestToSign(action.ID, owner)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	executed, err := svc.ApproveAction(context.Background(), action.ID, owner, signDigest(t, ownerKey, digest))
	if err != nil {
		t.Fatalf("expected the sole owner's own approval to meet threshold 1 and execute, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}
	if len(chain.submitted) != 2 {
		t.Fatalf("expected a deployment submission plus an execution submission, got %d", len(chain.submitted))
	}
	if chain.submitted[1].to != common.HexToAddress(*group.Address) {
		t.Fatalf("expected the execution to target the group's own Safe, got %s", chain.submitted[1].to.Hex())
	}
}

func TestCreateGroup_RejectsARegisteredUsersSignerAddress(t *testing.T) {
	db := newTestDB(t)
	pool, err := relayer.NewPool("test-relayer-salt", 2)
	if err != nil {
		t.Fatalf("relayer.NewPool: %v", err)
	}
	svc := New(db, newFakeBlockchain(""), "test-salt", 84532, pool)
	signerAddr, _ := randomAddress(t)
	primaryAddr, _ := randomAddress(t)
	user := usersModels.User{Username: "alice", Email: "alice@example.com", Address: primaryAddr, SignerAddress: signerAddr, PrimaryWalletDeployed: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err = svc.CreateGroup(context.Background(), "bad group", 1, []MemberInput{
		{Address: signerAddr, Role: models.RoleApprover},
	})
	if err == nil {
		t.Fatal("expected an error naming a registered user's raw signer address as an approver")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 400 {
		t.Fatalf("expected a 400 AppError, got %#v", err)
	}
}

func TestCreateGroup_RejectsAnUndeployedPrimaryWallet(t *testing.T) {
	db := newTestDB(t)
	pool, err := relayer.NewPool("test-relayer-salt", 2)
	if err != nil {
		t.Fatalf("relayer.NewPool: %v", err)
	}
	svc := New(db, newFakeBlockchain(""), "test-salt", 84532, pool)
	signerAddr, _ := randomAddress(t)
	primaryAddr, _ := randomAddress(t)
	user := usersModels.User{Username: "bob", Email: "bob@example.com", Address: primaryAddr, SignerAddress: signerAddr, PrimaryWalletDeployed: false}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err = svc.CreateGroup(context.Background(), "bad group", 1, []MemberInput{
		{Address: primaryAddr, Role: models.RoleApprover},
	})
	if err == nil {
		t.Fatal("expected an error naming an undeployed primary wallet as an approver")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 409 {
		t.Fatalf("expected a 409 AppError, got %#v", err)
	}
}

func TestCreateGroup_AcceptsADeployedPrimaryWalletAsMember(t *testing.T) {
	db := newTestDB(t)
	pool, err := relayer.NewPool("test-relayer-salt", 2)
	if err != nil {
		t.Fatalf("relayer.NewPool: %v", err)
	}
	chain := newFakeBlockchain("0xdeployed")
	svc := New(db, chain, "test-salt", 84532, pool)
	signerAddr, _ := randomAddress(t)
	primaryAddr, _ := randomAddress(t)
	user := usersModels.User{Username: "carol", Email: "carol@example.com", Address: primaryAddr, SignerAddress: signerAddr, PrimaryWalletDeployed: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err = svc.CreateGroup(context.Background(), "good group", 1, []MemberInput{
		{Address: primaryAddr, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("expected a deployed primary wallet to be accepted as a member, got: %v", err)
	}
}

func TestApproveAction_NestedPrimaryWalletMemberExecutesViaEIP1271(t *testing.T) {
	db := newTestDB(t)
	pool, err := relayer.NewPool("test-relayer-salt", 2)
	if err != nil {
		t.Fatalf("relayer.NewPool: %v", err)
	}
	chain := newFakeBlockchain("0xdeployed")
	svc := New(db, chain, "test-salt", 84532, pool)

	signerAddr, signerKey := randomAddress(t)
	primaryAddr, _ := randomAddress(t)
	user := usersModels.User{Username: "dave", Email: "dave@example.com", Address: primaryAddr, SignerAddress: signerAddr, PrimaryWalletDeployed: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "shared with dave", 1, []MemberInput{
		{Address: primaryAddr, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	action, err := svc.ProposePayment(context.Background(), primaryAddr, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}

	// The digest a nested primary-wallet member signs is the nested
	// EIP-1271 MessageHashForSafe hash, not the outer group's own
	// SafeTxHash - and it's signed by the user's current SignerAddress
	// key, not any key for the primary wallet itself (which has none).
	digest, err := svc.DigestToSign(action.ID, primaryAddr)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	tx, err := buildSafeTx(action)
	if err != nil {
		t.Fatalf("buildSafeTx returned error: %v", err)
	}
	directDigest := safe.SafeTxHash(safe.DomainSeparator(svc.ChainID, common.HexToAddress(*group.Address)), tx).Hex()
	if digest == directDigest {
		t.Fatal("expected the nested member's digest to differ from the outer SafeTxHash")
	}

	executed, err := svc.ApproveAction(context.Background(), action.ID, primaryAddr, signDigest(t, signerKey, digest))
	if err != nil {
		t.Fatalf("expected the nested approval to verify and execute, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}
}

func TestProposeAndApprove_ExecutesOnceThresholdIsMet(t *testing.T) {
	chain := newFakeBlockchain("0xdeployed")
	svc := newTestService(t, chain)

	initiator, _ := randomAddress(t)
	approver1, key1 := randomAddress(t)
	approver2, key2 := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "2-of-2", 2, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver1, Role: models.RoleApprover},
		{Address: approver2, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	action, err := svc.ProposePayment(context.Background(), initiator, group.ID, "monthly rent", recipient, "", "1000000000000000000")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	if action.Status != models.ActionPending {
		t.Fatalf("expected a pending action, got %s", action.Status)
	}

	ctx := context.Background()

	digest1, err := svc.DigestToSign(action.ID, approver1)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	// First approval: not yet enough to attempt execution at all.
	action, err = svc.ApproveAction(ctx, action.ID, approver1, signDigest(t, key1, digest1))
	if err != nil {
		t.Fatalf("first ApproveAction returned error: %v", err)
	}
	if action.Status != models.ActionPending {
		t.Fatalf("expected the action to remain pending after 1 of 2 approvals, got %s", action.Status)
	}

	digest2, err := svc.DigestToSign(action.ID, approver2)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	// Second approval reaches the threshold - the real Safe execTransaction
	// call is built, submitted, and confirmed.
	executed, err := svc.ApproveAction(ctx, action.ID, approver2, signDigest(t, key2, digest2))
	if err != nil {
		t.Fatalf("expected execution to succeed once the threshold is met, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}
	if executed.TxHash != "0xdeployed" {
		t.Fatalf("expected the execution tx hash to be recorded, got %q", executed.TxHash)
	}

	reloaded, err := svc.GetAction(action.ID)
	if err != nil {
		t.Fatalf("GetAction returned error: %v", err)
	}
	if reloaded.Status != models.ActionExecuted {
		t.Fatalf("expected the reloaded action to be EXECUTED, got %s", reloaded.Status)
	}
}

func TestApproveAction_RejectsForgedSignature(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xdeployed"))
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)
	_, impostorKey := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	action, err := svc.ProposePayment(context.Background(), initiator, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	digest, err := svc.DigestToSign(action.ID, approver)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}

	// approver claims to sign, but the signature actually comes from a
	// different key entirely (an attacker who isn't even a member trying
	// to forge the real approver's approval).
	forged := signDigest(t, impostorKey, digest)
	_, err = svc.ApproveAction(context.Background(), action.ID, approver, forged)
	if err == nil {
		t.Fatal("expected a forged signature to be rejected")
	}
}

func TestApproveAction_RejectsNonApprover(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xdeployed"))
	initiator, initiatorKey := randomAddress(t)
	approver, _ := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	action, err := svc.ProposePayment(context.Background(), initiator, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	digest, err := svc.DigestToSign(action.ID, initiator)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}

	// The initiator (not an APPROVER) tries to approve their own proposal.
	_, err = svc.ApproveAction(context.Background(), action.ID, initiator, signDigest(t, initiatorKey, digest))
	if err == nil {
		t.Fatal("expected an INITIATOR-only member to be rejected as an approver")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 403 {
		t.Fatalf("expected a 403 AppError, got %#v", err)
	}
}

func TestRejectAction(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xdeployed"))
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	action, err := svc.ProposePayment(context.Background(), initiator, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}

	rejected, err := svc.RejectAction(action.ID, approver, "looks wrong")
	if err != nil {
		t.Fatalf("RejectAction returned error: %v", err)
	}
	if rejected.Status != models.ActionRejected {
		t.Fatalf("expected rejected status, got %s", rejected.Status)
	}
	if rejected.RejectionReason != "looks wrong" {
		t.Fatalf("unexpected rejection reason: %s", rejected.RejectionReason)
	}
}

// TestApproveAction_RetryAfterFailedSubmissionDoesNotRequireASecondSignature
// covers a real gap found during manual smoke testing of the original
// custodial-key design: once every approver has approved but execution
// fails (a transient RPC error, here simulated via the fake blockchain's
// configurable err), the action must stay retryable without asking anyone
// to sign again - calling ApproveAction again with an already-recorded
// approval must retry the tally/execute attempt instead of rejecting it as
// a duplicate.
func TestApproveAction_RetryAfterFailedSubmissionDoesNotRequireASecondSignature(t *testing.T) {
	chain := newFakeBlockchain("0xexecuted")
	svc := newTestService(t, chain)

	initiator, _ := randomAddress(t)
	approver, approverKey := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	action, err := svc.ProposePayment(context.Background(), initiator, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	digest, err := svc.DigestToSign(action.ID, approver)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	sig := signDigest(t, approverKey, digest)

	ctx := context.Background()

	// The submission itself fails (a transient RPC error) - the approval
	// must still be recorded so a retry never re-asks the approver to sign
	// again.
	chain.err = fmt.Errorf("simulated RPC failure")
	if _, err := svc.ApproveAction(ctx, action.ID, approver, sig); err == nil {
		t.Fatal("expected the simulated submission failure to surface as an error")
	}
	var approvalCount int64
	svc.DB.Model(&models.PendingActionApproval{}).Where("pending_action_id = ?", action.ID).Count(&approvalCount)
	if approvalCount != 1 {
		t.Fatalf("expected the approval to be recorded despite execution failing, got count %d", approvalCount)
	}
	afterFailure, err := svc.GetAction(action.ID)
	if err != nil {
		t.Fatalf("GetAction returned error: %v", err)
	}
	if afterFailure.Status != models.ActionPending {
		t.Fatalf("expected the action to remain PENDING after a failed submission, got %s", afterFailure.Status)
	}

	// Retrying with the same already-recorded approval must go through the
	// retry branch (not a duplicate-approval rejection) and, once the
	// transient failure clears, succeed without a second signature.
	chain.err = nil
	executed, err := svc.ApproveAction(ctx, action.ID, approver, sig)
	if err != nil {
		t.Fatalf("expected the retry to succeed once the transient failure clears: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED after retry, got %s", executed.Status)
	}
}

func TestProposeAddMember_ApprovingMemberBecomesRealSafeOwner(t *testing.T) {
	chain := newFakeBlockchain("0xexecuted")
	svc := newTestService(t, chain)
	owner, ownerKey := randomAddress(t)
	newMember, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: owner, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	action, err := svc.ProposeAddMember(context.Background(), owner, group.ID, newMember, models.RoleApprover, 1)
	if err != nil {
		t.Fatalf("ProposeAddMember returned error: %v", err)
	}
	if action.To == "" {
		t.Fatal("expected an approving member's addition to be a real Safe call")
	}

	digest, err := svc.DigestToSign(action.ID, owner)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	executed, err := svc.ApproveAction(context.Background(), action.ID, owner, signDigest(t, ownerKey, digest))
	if err != nil {
		t.Fatalf("expected the add-member action to execute, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}
	if chain.submitted[len(chain.submitted)-1].to != common.HexToAddress(*group.Address) {
		t.Fatal("expected the addOwnerWithThreshold call to target the group's own Safe")
	}

	role, err := svc.memberRole(group.ID, newMember)
	if err != nil {
		t.Fatalf("expected the new member to be recorded locally, got error: %v", err)
	}
	if role != models.RoleApprover {
		t.Fatalf("expected role APPROVER, got %s", role)
	}
}

func TestProposeAddMember_NonApprovingMemberIsDBOnlyNoChainCall(t *testing.T) {
	chain := newFakeBlockchain("0xexecuted")
	svc := newTestService(t, chain)
	owner, ownerKey := randomAddress(t)
	newMember, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: owner, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	submittedBefore := len(chain.submitted)

	action, err := svc.ProposeAddMember(context.Background(), owner, group.ID, newMember, models.RoleViewOnly, group.Threshold)
	if err != nil {
		t.Fatalf("ProposeAddMember returned error: %v", err)
	}
	if action.To != "" {
		t.Fatal("expected a non-approving member's addition to never touch the Safe")
	}

	digest, err := svc.DigestToSign(action.ID, owner)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	executed, err := svc.ApproveAction(context.Background(), action.ID, owner, signDigest(t, ownerKey, digest))
	if err != nil {
		t.Fatalf("expected the DB-only add-member action to execute, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}
	if len(chain.submitted) != submittedBefore {
		t.Fatalf("expected no additional on-chain submission for a VIEW_ONLY addition, got %d new", len(chain.submitted)-submittedBefore)
	}
	role, err := svc.memberRole(group.ID, newMember)
	if err != nil {
		t.Fatalf("expected the new member to be recorded locally, got error: %v", err)
	}
	if role != models.RoleViewOnly {
		t.Fatalf("expected role VIEW_ONLY, got %s", role)
	}
}

func TestProposeRemoveMember_ApprovingMemberRemovedOnChain(t *testing.T) {
	chain := newFakeBlockchain("0xexecuted")
	svc := newTestService(t, chain)
	ownerA, keyA := randomAddress(t)
	ownerB, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "2-approvers-1-of-2", 1, []MemberInput{
		{Address: ownerA, Role: models.RoleInitiatorApprover},
		{Address: ownerB, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	// Simulate the Safe's real on-chain owner order for FindPrevOwner.
	chain.owners = []common.Address{common.HexToAddress(ownerA), common.HexToAddress(ownerB)}

	action, err := svc.ProposeRemoveMember(context.Background(), ownerA, group.ID, ownerB, 1)
	if err != nil {
		t.Fatalf("ProposeRemoveMember returned error: %v", err)
	}
	if action.To == "" {
		t.Fatal("expected an approving member's removal to be a real Safe call")
	}

	digest, err := svc.DigestToSign(action.ID, ownerA)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	executed, err := svc.ApproveAction(context.Background(), action.ID, ownerA, signDigest(t, keyA, digest))
	if err != nil {
		t.Fatalf("expected the remove-member action to execute, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}

	if _, err := svc.memberRole(group.ID, ownerB); err == nil {
		t.Fatal("expected the removed member to no longer be a group member")
	}
}

func TestProposeChangeThreshold_UpdatesGroupOnExecution(t *testing.T) {
	chain := newFakeBlockchain("0xexecuted")
	svc := newTestService(t, chain)
	ownerA, keyA := randomAddress(t)
	ownerB, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-2", 1, []MemberInput{
		{Address: ownerA, Role: models.RoleInitiatorApprover},
		{Address: ownerB, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	action, err := svc.ProposeChangeThreshold(context.Background(), ownerA, group.ID, 2)
	if err != nil {
		t.Fatalf("ProposeChangeThreshold returned error: %v", err)
	}

	digest, err := svc.DigestToSign(action.ID, ownerA)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	executed, err := svc.ApproveAction(context.Background(), action.ID, ownerA, signDigest(t, keyA, digest))
	if err != nil {
		t.Fatalf("expected the change-threshold action to execute, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}

	reloaded, err := svc.GetGroup(group.ID)
	if err != nil {
		t.Fatalf("GetGroup returned error: %v", err)
	}
	if reloaded.Threshold != 2 {
		t.Fatalf("expected threshold 2, got %d", reloaded.Threshold)
	}
}

func TestProposeChangeThreshold_RejectsExceedingApproverCount(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xexecuted"))
	owner, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: owner, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	_, err = svc.ProposeChangeThreshold(context.Background(), owner, group.ID, 5)
	if err == nil {
		t.Fatal("expected an error when the new threshold exceeds the number of approvers")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 400 {
		t.Fatalf("expected a 400 AppError, got %#v", err)
	}
}

func TestProposeDisableGroup_ExecutesWithNoChainCallAndBlocksFurtherProposals(t *testing.T) {
	chain := newFakeBlockchain("0xexecuted")
	svc := newTestService(t, chain)
	owner, ownerKey := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: owner, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	submittedBefore := len(chain.submitted)

	action, err := svc.ProposeDisableGroup(context.Background(), owner, group.ID)
	if err != nil {
		t.Fatalf("ProposeDisableGroup returned error: %v", err)
	}

	digest, err := svc.DigestToSign(action.ID, owner)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	executed, err := svc.ApproveAction(context.Background(), action.ID, owner, signDigest(t, ownerKey, digest))
	if err != nil {
		t.Fatalf("expected the disable action to execute, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}
	if len(chain.submitted) != submittedBefore {
		t.Fatal("expected disabling a group to never submit an on-chain transaction")
	}

	reloaded, err := svc.GetGroup(group.ID)
	if err != nil {
		t.Fatalf("GetGroup returned error: %v", err)
	}
	if !reloaded.Disabled {
		t.Fatal("expected the group to be marked disabled")
	}

	if _, err := svc.ProposePayment(context.Background(), owner, group.ID, "", recipient, "", "1"); err == nil {
		t.Fatal("expected proposing a payment on a disabled group to fail")
	}
}

func TestRequireNoConflictingManagementAction_BlocksWhileOneIsPending(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xexecuted"))
	owner, _ := randomAddress(t)
	newMember, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: owner, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	if _, err := svc.ProposeDisableGroup(context.Background(), owner, group.ID); err != nil {
		t.Fatalf("ProposeDisableGroup returned error: %v", err)
	}

	_, err = svc.ProposeAddMember(context.Background(), owner, group.ID, newMember, models.RoleViewOnly, 1)
	if err == nil {
		t.Fatal("expected a second management action to be blocked while the disable is still pending")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 409 {
		t.Fatalf("expected a 409 AppError, got %#v", err)
	}
}

func TestProposePayment_BlocksASecondOutstandingOnChainAction(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xdeployed"))
	initiator, _ := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if _, err := svc.ProposePayment(context.Background(), initiator, group.ID, "first", recipient, "", "1"); err != nil {
		t.Fatalf("first ProposePayment returned error: %v", err)
	}

	_, err = svc.ProposePayment(context.Background(), initiator, group.ID, "second", recipient, "", "1")
	if err == nil {
		t.Fatal("expected a second on-chain action to be blocked while the first is still outstanding")
	}
	appErr2, ok := err.(*apperrors.AppError)
	if !ok || appErr2.StatusCode() != 409 {
		t.Fatalf("expected a 409 AppError, got %#v", err)
	}
}

func TestProposePayment_AllowedAgainOnceThePriorActionIsResolved(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xdeployed"))
	initiator, initiatorKey := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	first, err := svc.ProposePayment(context.Background(), initiator, group.ID, "first", recipient, "", "1")
	if err != nil {
		t.Fatalf("first ProposePayment returned error: %v", err)
	}
	if _, err := svc.RejectAction(first.ID, initiator, "changed my mind"); err != nil {
		t.Fatalf("RejectAction returned error: %v", err)
	}

	second, err := svc.ProposePayment(context.Background(), initiator, group.ID, "second", recipient, "", "1")
	if err != nil {
		t.Fatalf("expected a second proposal to succeed once the first was rejected, got error: %v", err)
	}
	digest, err := svc.DigestToSign(second.ID, initiator)
	if err != nil {
		t.Fatalf("DigestToSign returned error: %v", err)
	}
	executed, err := svc.ApproveAction(context.Background(), second.ID, initiator, signDigest(t, initiatorKey, digest))
	if err != nil {
		t.Fatalf("expected the second action to execute cleanly, got error: %v", err)
	}
	if executed.Status != models.ActionExecuted {
		t.Fatalf("expected status EXECUTED, got %s", executed.Status)
	}
}

func TestExpireStalePendingActions_RejectsOnlyActionsOlderThanTTL(t *testing.T) {
	svc := newTestService(t, newFakeBlockchain("0xdeployed"))
	initiator, _ := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	stale, err := svc.ProposePayment(context.Background(), initiator, group.ID, "stale", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	if err := svc.DB.Model(&models.PendingAction{}).Where("id = ?", stale.ID).
		Update("created_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("failed to backdate the stale action: %v", err)
	}

	// A second on-chain proposal would normally be blocked while the
	// first (stale) one is outstanding - reject it first via the sweep,
	// exactly like a real timeout would, then confirm the group is usable
	// again.
	affected, err := svc.ExpireStalePendingActions(time.Hour)
	if err != nil {
		t.Fatalf("ExpireStalePendingActions returned error: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expected 1 action expired, got %d", affected)
	}

	reloaded, err := svc.GetAction(stale.ID)
	if err != nil {
		t.Fatalf("GetAction returned error: %v", err)
	}
	if reloaded.Status != models.ActionRejected {
		t.Fatalf("expected the stale action to be REJECTED, got %s", reloaded.Status)
	}

	if _, err := svc.ProposePayment(context.Background(), initiator, group.ID, "fresh", recipient, "", "1"); err != nil {
		t.Fatalf("expected a fresh proposal to succeed once the stale one expired, got error: %v", err)
	}
}
