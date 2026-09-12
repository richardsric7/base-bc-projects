package services

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/sharedaccess/models"
)

// fakeBlockchain is a BlockchainClient that never touches the network -
// SignAndSubmitTx just records the call and returns a canned hash, so the
// approval-threshold execution path is testable without a live Base RPC.
type fakeBlockchain struct {
	submittedTo   *common.Address
	submittedData []byte
	hash          string
	err           error
}

func (f *fakeBlockchain) SignAndSubmitTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, _ *big.Int, data []byte, _ *uint64) (string, error) {
	f.submittedTo = to
	f.submittedData = data
	if f.err != nil {
		return "", f.err
	}
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

func TestCreateGroup_DerivesDistinctDeterministicAddress(t *testing.T) {
	svc := New(newTestDB(t), &fakeBlockchain{}, "test-salt")
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)

	group, err := svc.CreateGroup("family wallet", 1, []MemberInput{
		{Address: initiator, Role: models.RoleInitiator},
		{Address: approver, Role: models.RoleApprover},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if group.Address == nil || *group.Address == "" {
		t.Fatal("expected a derived group address")
	}
	if group.Purpose != models.PurposeWalletAccess {
		t.Fatalf("expected purpose WALLET_ACCESS, got %q", group.Purpose)
	}

	other, err := svc.CreateGroup("other wallet", 1, []MemberInput{
		{Address: approver, Role: models.RoleApprover},
		{Address: initiator, Role: models.RoleInitiator},
	})
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if *other.Address == *group.Address {
		t.Fatal("expected different groups to derive different addresses")
	}
}

func TestCreateGroup_ThresholdExceedsApprovers(t *testing.T) {
	svc := New(newTestDB(t), &fakeBlockchain{}, "test-salt")
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)

	_, err := svc.CreateGroup("bad group", 2, []MemberInput{
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

func TestProposeApproveExecute_TwoOfTwoThreshold(t *testing.T) {
	chain := &fakeBlockchain{hash: "0xdeadbeef"}
	svc := New(newTestDB(t), chain, "test-salt")

	initiator, _ := randomAddress(t)
	approver1, key1 := randomAddress(t)
	approver2, key2 := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup("2-of-2", 2, []MemberInput{
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

	// First approval: not yet enough to execute.
	action, err = svc.ApproveAction(ctx, action.ID, approver1, sign(t, key1, message))
	if err != nil {
		t.Fatalf("first ApproveAction returned error: %v", err)
	}
	if action.Status != models.ActionPending {
		t.Fatalf("expected the action to remain pending after 1 of 2 approvals, got %s", action.Status)
	}
	if chain.submittedTo != nil {
		t.Fatal("expected no execution before the threshold is met")
	}

	// Second approval: threshold met, should execute.
	action, err = svc.ApproveAction(ctx, action.ID, approver2, sign(t, key2, message))
	if err != nil {
		t.Fatalf("second ApproveAction returned error: %v", err)
	}
	if action.Status != models.ActionExecuted {
		t.Fatalf("expected the action to execute at threshold, got %s", action.Status)
	}
	if action.TxHash != "0xdeadbeef" {
		t.Fatalf("unexpected tx hash: %s", action.TxHash)
	}
	if chain.submittedTo == nil || chain.submittedTo.Hex() != recipient {
		t.Fatalf("expected execution to submit to %s, got %v", recipient, chain.submittedTo)
	}
}

func TestApproveAction_RejectsForgedSignature(t *testing.T) {
	svc := New(newTestDB(t), &fakeBlockchain{}, "test-salt")
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)
	impostor, impostorKey := randomAddress(t)
	recipient, _ := randomAddress(t)
	_ = impostor

	group, err := svc.CreateGroup("1-of-1", 1, []MemberInput{
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
	svc := New(newTestDB(t), &fakeBlockchain{}, "test-salt")
	initiator, initiatorKey := randomAddress(t)
	approver, _ := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup("1-of-1", 1, []MemberInput{
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
	svc := New(newTestDB(t), &fakeBlockchain{}, "test-salt")
	initiator, _ := randomAddress(t)
	approver, _ := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup("1-of-1", 1, []MemberInput{
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

// TestApproveAction_RetriesExecutionAfterFailure covers a real gap found
// during manual smoke testing: once every approver has approved but the
// on-chain submission fails (e.g. a transient RPC error), the action stays
// PENDING with every approver slot already filled. Without a retry path,
// it would be permanently stuck - nobody left who could produce a "new"
// approval. Calling ApproveAction again with an already-recorded approval
// must retry execution instead of rejecting it as a duplicate.
func TestApproveAction_RetriesExecutionAfterFailure(t *testing.T) {
	chain := &fakeBlockchain{err: errors.New("rpc unreachable")}
	svc := New(newTestDB(t), chain, "test-salt")

	initiator, _ := randomAddress(t)
	approver, approverKey := randomAddress(t)
	recipient, _ := randomAddress(t)

	group, err := svc.CreateGroup("1-of-1", 1, []MemberInput{
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

	// First approval reaches the 1-of-1 threshold but execution fails; the
	// caller correctly sees an error, but the underlying action must stay
	// PENDING (not stuck in some broken intermediate state) so a retry is
	// possible.
	_, err = svc.ApproveAction(ctx, action.ID, approver, sig)
	if err == nil {
		t.Fatal("expected an error when the blockchain call fails")
	}
	action, err = svc.GetAction(action.ID)
	if err != nil {
		t.Fatalf("GetAction returned error: %v", err)
	}
	if action.Status != models.ActionPending {
		t.Fatalf("expected the action to remain pending after a failed execution, got %s", action.Status)
	}

	// The RPC recovers; approving again (same member, already on record)
	// must retry execution rather than erroring as a duplicate approval.
	chain.err = nil
	chain.hash = "0xrecovered"
	action, err = svc.ApproveAction(ctx, action.ID, approver, sig)
	if err != nil {
		t.Fatalf("expected the retry to succeed, got error: %v", err)
	}
	if action.Status != models.ActionExecuted {
		t.Fatalf("expected the retried execution to succeed, got status %s", action.Status)
	}
	if action.TxHash != "0xrecovered" {
		t.Fatalf("unexpected tx hash: %s", action.TxHash)
	}
}
