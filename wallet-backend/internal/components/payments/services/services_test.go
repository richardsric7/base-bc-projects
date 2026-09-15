package services

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/payments/models"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
)

const (
	testWallet = "0x1111111111111111111111111111111111111111"
	testSigner = "0x2222222222222222222222222222222222222222"
	testTo     = "0x3333333333333333333333333333333333333333"
)

// fakeGroupWalletExecutor is a scriptable GroupWalletExecutor - lets each
// test control exactly what sharedaccess would have returned without a
// real database/chain, and records what it was called with so a test can
// assert the right proposer/wallet/action flowed through.
type fakeGroupWalletExecutor struct {
	group    *sharedaccessModels.ClosedGroup
	groupErr error

	proposedAction  *sharedaccessModels.PendingAction
	proposeErr      error
	lastProposer    string
	lastGroupID     uint
	lastRecipient   string
	lastDescription string

	digest    string
	digestErr error

	approvedAction *sharedaccessModels.PendingAction
	approveErr     error
	approveCalled  bool
}

func (f *fakeGroupWalletExecutor) GetGroupByAddress(address string) (*sharedaccessModels.ClosedGroup, error) {
	if f.groupErr != nil {
		return nil, f.groupErr
	}
	return f.group, nil
}

func (f *fakeGroupWalletExecutor) ProposePayment(ctx context.Context, proposerAddress string, groupID uint, description, recipient, tokenAddress, amount, domain, relatedRecordID string) (*sharedaccessModels.PendingAction, error) {
	f.lastProposer = proposerAddress
	f.lastGroupID = groupID
	f.lastRecipient = recipient
	f.lastDescription = description
	if f.proposeErr != nil {
		return nil, f.proposeErr
	}
	return f.proposedAction, nil
}

func (f *fakeGroupWalletExecutor) DigestToSign(actionID uint, memberAddress string) (string, error) {
	if f.digestErr != nil {
		return "", f.digestErr
	}
	return f.digest, nil
}

func (f *fakeGroupWalletExecutor) ApproveAction(ctx context.Context, actionID uint, memberAddress, signatureHex string) (*sharedaccessModels.PendingAction, error) {
	f.approveCalled = true
	if f.approveErr != nil {
		return nil, f.approveErr
	}
	return f.approvedAction, nil
}

// fakeRecipientResolver is a pass-through RecipientResolver for tests that
// don't exercise alias/username/email resolution itself - it just hands
// back whatever identifier it was given, as if it were already an address.
type fakeRecipientResolver struct{}

func (fakeRecipientResolver) ResolveRecipient(identifier string) (string, error) {
	return identifier, nil
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

func TestBuildPaymentTx_FailsClosedWhenSharedAccessMissing(t *testing.T) {
	svc := New(newTestDB(t))
	_, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, testTo, "", "1000", "")
	if err == nil {
		t.Fatal("expected an error when SharedAccess is unwired")
	}
}

func TestBuildPaymentTx_FailsClosedWhenRecipientsMissing(t *testing.T) {
	svc := New(newTestDB(t))
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	_, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, testTo, "", "1000", "")
	if err == nil {
		t.Fatal("expected an error when Recipients is unwired")
	}
}

// aliasRecipientResolver resolves one fixed identifier ("alice") to
// testTo and errors on anything else, so a test can prove BuildPaymentTx
// actually routes `to` through RecipientResolver before validating it as
// an address, rather than requiring `to` to already be one.
type aliasRecipientResolver struct{ err error }

func (a aliasRecipientResolver) ResolveRecipient(identifier string) (string, error) {
	if a.err != nil {
		return "", a.err
	}
	if identifier == "alice" {
		return testTo, nil
	}
	return "", apperrors.NotFound("no such wallet, username, email, or alias")
}

func TestBuildPaymentTx_PropagatesRecipientResolutionError(t *testing.T) {
	svc := New(newTestDB(t))
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	svc.Recipients = aliasRecipientResolver{}
	if _, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, "not-alice-or-an-address", "", "1000", ""); err == nil {
		t.Fatal("expected the recipient resolution error to propagate")
	}
}

func TestBuildPaymentTx_ResolvesRecipientBeforeValidating(t *testing.T) {
	svc := New(newTestDB(t))
	fake := &fakeGroupWalletExecutor{
		group:          &sharedaccessModels.ClosedGroup{ID: 7},
		proposedAction: &sharedaccessModels.PendingAction{ID: 42},
		digest:         "0xdeadbeef",
	}
	svc.SharedAccess = fake
	svc.Recipients = aliasRecipientResolver{}

	proposal, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, "alice", "", "1000", "")
	if err != nil {
		t.Fatalf("unexpected error resolving a username/alias recipient: %v", err)
	}
	if proposal.ResolvedAddress != testTo {
		t.Fatalf("expected ResolvedAddress %s, got %s", testTo, proposal.ResolvedAddress)
	}
	if fake.lastRecipient != testTo {
		t.Fatalf("expected the resolved address to be proposed, got %s", fake.lastRecipient)
	}
}

func TestBuildPaymentTx_RejectsInvalidAddress(t *testing.T) {
	svc := New(newTestDB(t))
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	svc.Recipients = fakeRecipientResolver{}
	if _, err := svc.BuildPaymentTx(context.Background(), "not-an-address", testSigner, testTo, "", "1000", ""); err == nil {
		t.Fatal("expected an error for an invalid wallet address")
	}
	if _, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, "not-an-address", "", "1000", ""); err == nil {
		t.Fatal("expected an error for an invalid recipient address")
	}
}

func TestBuildPaymentTx_RejectsNonDecimalAmount(t *testing.T) {
	svc := New(newTestDB(t))
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	svc.Recipients = fakeRecipientResolver{}
	if _, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, testTo, "", "not-a-number", ""); err == nil {
		t.Fatal("expected an error for a non-decimal amount")
	}
}

func TestBuildPaymentTx_PropagatesGroupLookupError(t *testing.T) {
	svc := New(newTestDB(t))
	fake := &fakeGroupWalletExecutor{groupErr: apperrors.NotFound("no group")}
	svc.SharedAccess = fake
	svc.Recipients = fakeRecipientResolver{}
	if _, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, testTo, "", "1000", ""); err == nil {
		t.Fatal("expected the group lookup error to propagate")
	}
}

func TestBuildPaymentTx_Success(t *testing.T) {
	svc := New(newTestDB(t))
	fake := &fakeGroupWalletExecutor{
		group:          &sharedaccessModels.ClosedGroup{ID: 7},
		proposedAction: &sharedaccessModels.PendingAction{ID: 42},
		digest:         "0xdeadbeef",
	}
	svc.SharedAccess = fake
	svc.Recipients = fakeRecipientResolver{}

	proposal, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, testTo, "", "1000", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proposal.ActionID != 42 || proposal.DigestToSign != "0xdeadbeef" {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	// The proposal must be authorized by the caller's signer key, never
	// the wallet address itself - the wallet is a Safe with no key of its
	// own to ever be a valid group member/proposer.
	if fake.lastProposer != testSigner {
		t.Fatalf("expected proposer %s, got %s", testSigner, fake.lastProposer)
	}
	if fake.lastGroupID != 7 {
		t.Fatalf("expected groupID 7, got %d", fake.lastGroupID)
	}
	if fake.lastRecipient != testTo {
		t.Fatalf("expected recipient %s, got %s", testTo, fake.lastRecipient)
	}
	if fake.lastDescription != "payment" {
		t.Fatalf("expected the default description \"payment\" when no memo is given, got %q", fake.lastDescription)
	}
}

// TestBuildPaymentTx_MemoBecomesProposalDescription verifies PLAN.md
// §23's restored memo field: a non-empty memo becomes the sharedaccess
// proposal's own description (what an approver sees), replacing the
// default "payment" literal.
func TestBuildPaymentTx_MemoBecomesProposalDescription(t *testing.T) {
	svc := New(newTestDB(t))
	fake := &fakeGroupWalletExecutor{
		group:          &sharedaccessModels.ClosedGroup{ID: 7},
		proposedAction: &sharedaccessModels.PendingAction{ID: 42},
		digest:         "0xdeadbeef",
	}
	svc.SharedAccess = fake
	svc.Recipients = fakeRecipientResolver{}

	if _, err := svc.BuildPaymentTx(context.Background(), testWallet, testSigner, testTo, "", "1000", "invoice #4471"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastDescription != "invoice #4471" {
		t.Fatalf("expected the memo to become the proposal description, got %q", fake.lastDescription)
	}
}

func TestSubmitPayment_FailsClosedWhenSharedAccessMissing(t *testing.T) {
	svc := New(newTestDB(t))
	_, err := svc.SubmitPayment(context.Background(), "idem-1", 42, testSigner, "0xsig", testWallet, testTo, "", "1000", "")
	if err == nil {
		t.Fatal("expected an error when SharedAccess is unwired")
	}
}

func TestSubmitPayment_Success(t *testing.T) {
	svc := New(newTestDB(t))
	fake := &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 42, Status: sharedaccessModels.ActionExecuted, TxHash: "0xhash"},
	}
	svc.SharedAccess = fake

	record, err := svc.SubmitPayment(context.Background(), "idem-1", 42, testSigner, "0xsig", testWallet, testTo, "", "1000", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.TxHash != "0xhash" || record.FromAddress != testWallet || record.ToAddress != testTo {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.Memo != "" {
		t.Fatalf("expected an empty memo when none was given, got %q", record.Memo)
	}
	if !fake.approveCalled {
		t.Fatal("expected ApproveAction to be called")
	}
}

// TestSubmitPayment_PersistsMemo verifies PLAN.md §23's restored memo
// field is actually written to the PaymentHistory record, not just
// accepted and discarded.
func TestSubmitPayment_PersistsMemo(t *testing.T) {
	svc := New(newTestDB(t))
	fake := &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 42, Status: sharedaccessModels.ActionExecuted, TxHash: "0xhash"},
	}
	svc.SharedAccess = fake

	record, err := svc.SubmitPayment(context.Background(), "idem-1", 42, testSigner, "0xsig", testWallet, testTo, "", "1000", "invoice #4471")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Memo != "invoice #4471" {
		t.Fatalf("expected the memo to be persisted on the history record, got %q", record.Memo)
	}
}

func TestSubmitPayment_RejectsWhenNotYetExecuted(t *testing.T) {
	svc := New(newTestDB(t))
	svc.SharedAccess = &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 42, Status: sharedaccessModels.ActionPending},
	}
	if _, err := svc.SubmitPayment(context.Background(), "idem-1", 42, testSigner, "0xsig", testWallet, testTo, "", "1000", ""); err == nil {
		t.Fatal("expected an error when the action has not reached EXECUTED")
	}
}

func TestSubmitPayment_PropagatesApprovalError(t *testing.T) {
	svc := New(newTestDB(t))
	svc.SharedAccess = &fakeGroupWalletExecutor{approveErr: apperrors.Unauthorized("bad signature")}
	if _, err := svc.SubmitPayment(context.Background(), "idem-1", 42, testSigner, "0xsig", testWallet, testTo, "", "1000", ""); err == nil {
		t.Fatal("expected the approval error to propagate")
	}
}

func TestSubmitPayment_IdempotentRetryReturnsOriginalRecordWithoutReapproving(t *testing.T) {
	svc := New(newTestDB(t))
	fake := &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 42, Status: sharedaccessModels.ActionExecuted, TxHash: "0xhash"},
	}
	svc.SharedAccess = fake

	first, err := svc.SubmitPayment(context.Background(), "idem-1", 42, testSigner, "0xsig", testWallet, testTo, "", "1000", "")
	if err != nil {
		t.Fatalf("unexpected error on first submit: %v", err)
	}
	fake.approveCalled = false

	second, err := svc.SubmitPayment(context.Background(), "idem-1", 42, testSigner, "0xsig", testWallet, testTo, "", "1000", "")
	if err != nil {
		t.Fatalf("unexpected error on retry: %v", err)
	}
	if second.ID != first.ID || second.TxHash != first.TxHash {
		t.Fatalf("expected the same record back, got %+v vs %+v", first, second)
	}
	if fake.approveCalled {
		t.Fatal("expected a retried submission to short-circuit before calling ApproveAction again")
	}
}

func TestHistory_ReturnsSentAndReceivedPayments(t *testing.T) {
	db := newTestDB(t)
	svc := New(db)
	if err := db.Create(&models.PaymentHistory{IdempotencyKey: "a", FromAddress: testWallet, ToAddress: testTo, Amount: "1", TxHash: "0x1"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Create(&models.PaymentHistory{IdempotencyKey: "b", FromAddress: testTo, ToAddress: testWallet, Amount: "2", TxHash: "0x2"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	history, err := svc.History(testWallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 records, got %d", len(history))
	}
}
