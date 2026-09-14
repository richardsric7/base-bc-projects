package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/sharedaccess/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/safe"
)

// fakeBlockchain is a BlockchainClient that never touches the network -
// SignAndSubmitTx just records every submission (CreateGroup's own Safe
// deployment included) and returns a canned hash.
type fakeBlockchain struct {
	submitted []fakeSubmission
	hash      string
	err       error
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

func randomAddress(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), key
}

func sign(t *testing.T, key *ecdsa.PrivateKey, message string) string {
	t.Helper()
	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return "0x" + common.Bytes2Hex(sig)
}

func TestCreateGroup_DeploysARealSafe(t *testing.T) {
	chain := &fakeBlockchain{hash: "0xdeployed"}
	svc := New(newTestDB(t), chain, "test-salt")
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
	svc := New(newTestDB(t), &fakeBlockchain{}, "test-salt")
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
	chain := &fakeBlockchain{hash: "0xdeployed"}
	svc := New(newTestDB(t), chain, "test-salt")
	owner, ownerKey := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup(context.Background(), "sub-wallet", 1, []MemberInput{
		{Address: owner, Role: models.RoleInitiatorApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	action, err := svc.ProposePayment(owner, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment (as INITIATOR_APPROVER) returned error: %v", err)
	}
	message, err := svc.CanonicalActionMessage(action.ID)
	if err != nil {
		t.Fatalf("CanonicalActionMessage returned error: %v", err)
	}
	if _, err := svc.ApproveAction(context.Background(), action.ID, owner, sign(t, ownerKey, message)); err == nil {
		t.Fatal("expected the not-yet-implemented execution error once the sole owner's own approval meets threshold 1")
	}
}

func TestCreateGroup_RejectsARegisteredUsersSignerAddress(t *testing.T) {
	db := newTestDB(t)
	svc := New(db, &fakeBlockchain{}, "test-salt")
	signerAddr, _ := randomAddress(t)
	primaryAddr, _ := randomAddress(t)
	user := usersModels.User{Username: "alice", Email: "alice@example.com", Address: primaryAddr, SignerAddress: signerAddr, PrimaryWalletDeployed: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err := svc.CreateGroup(context.Background(), "bad group", 1, []MemberInput{
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
	svc := New(db, &fakeBlockchain{}, "test-salt")
	signerAddr, _ := randomAddress(t)
	primaryAddr, _ := randomAddress(t)
	user := usersModels.User{Username: "bob", Email: "bob@example.com", Address: primaryAddr, SignerAddress: signerAddr, PrimaryWalletDeployed: false}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err := svc.CreateGroup(context.Background(), "bad group", 1, []MemberInput{
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
	chain := &fakeBlockchain{hash: "0xdeployed"}
	svc := New(db, chain, "test-salt")
	signerAddr, _ := randomAddress(t)
	primaryAddr, _ := randomAddress(t)
	user := usersModels.User{Username: "carol", Email: "carol@example.com", Address: primaryAddr, SignerAddress: signerAddr, PrimaryWalletDeployed: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	_, err := svc.CreateGroup(context.Background(), "good group", 1, []MemberInput{
		{Address: primaryAddr, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("expected a deployed primary wallet to be accepted as a member, got: %v", err)
	}
}

func TestProposeAndApprove_TalliesButExecutionIsNotYetImplemented(t *testing.T) {
	chain := &fakeBlockchain{hash: "0xdeployed"}
	svc := New(newTestDB(t), chain, "test-salt")

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

	action, err := svc.ProposePayment(initiator, group.ID, "monthly rent", recipient, "", "1000000000000000000")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	if action.Status != models.ActionPending {
		t.Fatalf("expected a pending action, got %s", action.Status)
	}

	message, err := svc.CanonicalActionMessage(action.ID)
	if err != nil {
		t.Fatalf("CanonicalActionMessage returned error: %v", err)
	}

	ctx := context.Background()

	// First approval: not yet enough to attempt execution at all.
	action, err = svc.ApproveAction(ctx, action.ID, approver1, sign(t, key1, message))
	if err != nil {
		t.Fatalf("first ApproveAction returned error: %v", err)
	}
	if action.Status != models.ActionPending {
		t.Fatalf("expected the action to remain pending after 1 of 2 approvals, got %s", action.Status)
	}

	// Second approval reaches the threshold - execution is attempted and
	// fails loudly (PLAN.md §13.10 Phase 4 hasn't wired real Safe
	// execTransaction submission yet), rather than silently doing
	// something meaningless with the group's Safe.
	if _, err := svc.ApproveAction(ctx, action.ID, approver2, sign(t, key2, message)); err == nil {
		t.Fatal("expected execution to report not-yet-implemented once the threshold is met")
	}

	reloaded, err := svc.GetAction(action.ID)
	if err != nil {
		t.Fatalf("GetAction returned error: %v", err)
	}
	if reloaded.Status != models.ActionPending {
		t.Fatalf("expected the action to remain PENDING (not silently marked executed), got %s", reloaded.Status)
	}
}

func TestApproveAction_RejectsForgedSignature(t *testing.T) {
	svc := New(newTestDB(t), &fakeBlockchain{hash: "0xdeployed"}, "test-salt")
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)
	impostor, impostorKey := randomAddress(t)
	recipient, _ := randomAddress(t)
	_ = impostor

	group, err := svc.CreateGroup(context.Background(), "1-of-1", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	action, err := svc.ProposePayment(initiator, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	message, err := svc.CanonicalActionMessage(action.ID)
	if err != nil {
		t.Fatalf("CanonicalActionMessage returned error: %v", err)
	}

	// approver claims to sign, but the signature actually comes from a
	// different key entirely (an attacker who isn't even a member trying
	// to forge the real approver's approval).
	forged := sign(t, impostorKey, message)
	_, err = svc.ApproveAction(context.Background(), action.ID, approver, forged)
	if err == nil {
		t.Fatal("expected a forged signature to be rejected")
	}
}

func TestApproveAction_RejectsNonApprover(t *testing.T) {
	svc := New(newTestDB(t), &fakeBlockchain{hash: "0xdeployed"}, "test-salt")
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
	action, err := svc.ProposePayment(initiator, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	message, err := svc.CanonicalActionMessage(action.ID)
	if err != nil {
		t.Fatalf("CanonicalActionMessage returned error: %v", err)
	}

	// The initiator (not an APPROVER) tries to approve their own proposal.
	_, err = svc.ApproveAction(context.Background(), action.ID, initiator, sign(t, initiatorKey, message))
	if err == nil {
		t.Fatal("expected an INITIATOR-only member to be rejected as an approver")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 403 {
		t.Fatalf("expected a 403 AppError, got %#v", err)
	}
}

func TestRejectAction(t *testing.T) {
	svc := New(newTestDB(t), &fakeBlockchain{hash: "0xdeployed"}, "test-salt")
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
	action, err := svc.ProposePayment(initiator, group.ID, "", recipient, "", "1")
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

// TestApproveAction_RetryDoesNotRequireASecondSignature covers a real gap
// found during manual smoke testing of the original custodial-key design:
// once every approver has approved, the action stays PENDING (now always,
// since real execution isn't wired up yet - see executeAction's doc
// comment). Calling ApproveAction again with an already-recorded approval
// must retry the tally/execute attempt instead of rejecting it as a
// duplicate - otherwise every approver having already signed once would
// permanently strand the action with no way to move it forward once real
// execution does land.
func TestApproveAction_RetryDoesNotRequireASecondSignature(t *testing.T) {
	chain := &fakeBlockchain{hash: "0xdeployed"}
	svc := New(newTestDB(t), chain, "test-salt")

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
	action, err := svc.ProposePayment(initiator, group.ID, "", recipient, "", "1")
	if err != nil {
		t.Fatalf("ProposePayment returned error: %v", err)
	}
	message, err := svc.CanonicalActionMessage(action.ID)
	if err != nil {
		t.Fatalf("CanonicalActionMessage returned error: %v", err)
	}
	sig := sign(t, approverKey, message)

	ctx := context.Background()

	// First approval reaches the 1-of-1 threshold; execution is attempted
	// and fails with the expected not-yet-implemented error, but the
	// approval itself must still be recorded so a retry never re-asks the
	// approver to sign again.
	if _, err := svc.ApproveAction(ctx, action.ID, approver, sig); err == nil {
		t.Fatal("expected the not-yet-implemented execution error")
	}
	var approvalCount int64
	svc.DB.Model(&models.PendingActionApproval{}).Where("pending_action_id = ?", action.ID).Count(&approvalCount)
	if approvalCount != 1 {
		t.Fatalf("expected the approval to be recorded despite execution failing, got count %d", approvalCount)
	}

	// Retrying with the same already-recorded approval must go through
	// the retry branch (not a duplicate-approval rejection) and hit the
	// same consistent error.
	if _, err := svc.ApproveAction(ctx, action.ID, approver, sig); err == nil {
		t.Fatal("expected the retry to also report not-yet-implemented")
	}

	reloaded, err := svc.GetAction(action.ID)
	if err != nil {
		t.Fatalf("GetAction returned error: %v", err)
	}
	if reloaded.Status != models.ActionPending {
		t.Fatalf("expected the action to remain PENDING across retries, got %s", reloaded.Status)
	}
}
