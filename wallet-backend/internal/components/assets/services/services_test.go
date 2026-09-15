package services

import (
	"context"
	"strings"
	"testing"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
)

const (
	testWallet  = "0x1111111111111111111111111111111111111111"
	testSigner  = "0x2222222222222222222222222222222222222222"
	testToken   = "0x3333333333333333333333333333333333333333"
	testSpender = "0x4444444444444444444444444444444444444444"
)

type fakeGroupWalletExecutor struct {
	group    *sharedaccessModels.ClosedGroup
	groupErr error

	proposedAction *sharedaccessModels.PendingAction
	proposeErr     error
	lastProposer   string
	lastGroupID    uint
	lastDataHex    string

	digest    string
	digestErr error

	approvedAction *sharedaccessModels.PendingAction
	approveErr     error
}

func (f *fakeGroupWalletExecutor) GetGroupByAddress(address string) (*sharedaccessModels.ClosedGroup, error) {
	if f.groupErr != nil {
		return nil, f.groupErr
	}
	return f.group, nil
}

func (f *fakeGroupWalletExecutor) ProposeContractCall(ctx context.Context, proposerAddress string, groupID uint, kind sharedaccessModels.ActionKind, description, contractAddress, valueWei, dataHex, domain, relatedRecordID string) (*sharedaccessModels.PendingAction, error) {
	f.lastProposer = proposerAddress
	f.lastGroupID = groupID
	f.lastDataHex = dataHex
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
	if f.approveErr != nil {
		return nil, f.approveErr
	}
	return f.approvedAction, nil
}

func TestBuildApproveTx_FailsClosedWhenSharedAccessMissing(t *testing.T) {
	svc := New(nil, nil)
	_, err := svc.BuildApproveTx(context.Background(), testWallet, testSigner, testToken, testSpender, "1000")
	if err == nil {
		t.Fatal("expected an error when SharedAccess is unwired")
	}
}

func TestBuildApproveTx_RejectsInvalidAddresses(t *testing.T) {
	svc := New(nil, nil)
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	if _, err := svc.BuildApproveTx(context.Background(), "bad", testSigner, testToken, testSpender, "1000"); err == nil {
		t.Fatal("expected an error for an invalid wallet address")
	}
	if _, err := svc.BuildApproveTx(context.Background(), testWallet, testSigner, "bad", testSpender, "1000"); err == nil {
		t.Fatal("expected an error for an invalid token address")
	}
	if _, err := svc.BuildApproveTx(context.Background(), testWallet, testSigner, testToken, "bad", "1000"); err == nil {
		t.Fatal("expected an error for an invalid spender address")
	}
}

func TestBuildApproveTx_RejectsNonDecimalAmount(t *testing.T) {
	svc := New(nil, nil)
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	if _, err := svc.BuildApproveTx(context.Background(), testWallet, testSigner, testToken, testSpender, "not-a-number"); err == nil {
		t.Fatal("expected an error for a non-decimal amount")
	}
}

func TestBuildApproveTx_Success(t *testing.T) {
	fake := &fakeGroupWalletExecutor{
		group:          &sharedaccessModels.ClosedGroup{ID: 3},
		proposedAction: &sharedaccessModels.PendingAction{ID: 21},
		digest:         "0xdigest",
	}
	svc := New(nil, nil)
	svc.SharedAccess = fake

	proposal, err := svc.BuildApproveTx(context.Background(), testWallet, testSigner, testToken, testSpender, "1000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proposal.ActionID != 21 || proposal.DigestToSign != "0xdigest" {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	if fake.lastProposer != testSigner {
		t.Fatalf("expected proposer %s, got %s", testSigner, fake.lastProposer)
	}
	if fake.lastGroupID != 3 {
		t.Fatalf("expected groupID 3, got %d", fake.lastGroupID)
	}
	// Real ABI-encoded approve(address,uint256) selector - proves this is
	// genuinely encoded, not a placeholder.
	if !strings.HasPrefix(fake.lastDataHex, "0x095ea7b3") {
		t.Fatalf("expected the real approve() selector, got %q", fake.lastDataHex)
	}
}

func TestBuildApproveTx_PropagatesGroupLookupError(t *testing.T) {
	svc := New(nil, nil)
	svc.SharedAccess = &fakeGroupWalletExecutor{groupErr: apperrors.NotFound("no group")}
	if _, err := svc.BuildApproveTx(context.Background(), testWallet, testSigner, testToken, testSpender, "1000"); err == nil {
		t.Fatal("expected the group lookup error to propagate")
	}
}

func TestSubmitApprove_FailsClosedWhenSharedAccessMissing(t *testing.T) {
	svc := New(nil, nil)
	if _, err := svc.SubmitApprove(context.Background(), 21, testSigner, "0xsig"); err == nil {
		t.Fatal("expected an error when SharedAccess is unwired")
	}
}

func TestSubmitApprove_Success(t *testing.T) {
	svc := New(nil, nil)
	svc.SharedAccess = &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 21, Status: sharedaccessModels.ActionExecuted, TxHash: "0xhash"},
	}
	hash, err := svc.SubmitApprove(context.Background(), 21, testSigner, "0xsig")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "0xhash" {
		t.Fatalf("expected 0xhash, got %s", hash)
	}
}

func TestSubmitApprove_RejectsWhenNotYetExecuted(t *testing.T) {
	svc := New(nil, nil)
	svc.SharedAccess = &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 21, Status: sharedaccessModels.ActionPending},
	}
	if _, err := svc.SubmitApprove(context.Background(), 21, testSigner, "0xsig"); err == nil {
		t.Fatal("expected an error when the action has not reached EXECUTED")
	}
}

func TestSubmitApprove_PropagatesApprovalError(t *testing.T) {
	svc := New(nil, nil)
	svc.SharedAccess = &fakeGroupWalletExecutor{approveErr: apperrors.Unauthorized("bad signature")}
	if _, err := svc.SubmitApprove(context.Background(), 21, testSigner, "0xsig"); err == nil {
		t.Fatal("expected the approval error to propagate")
	}
}
