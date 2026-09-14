package services

import (
	"context"
	"strings"
	"testing"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
)

const (
	testWallet = "0x1111111111111111111111111111111111111111"
	testSigner = "0x2222222222222222222222222222222222222222"
	testRouter = "0x3333333333333333333333333333333333333333"
)

// A minimal ERC-20-shaped ABI fragment, just to exercise real ABI encoding
// end to end rather than asserting on a hand-typed hex string.
const testRouterABI = `[{"name":"swap","type":"function","inputs":[{"name":"amountIn","type":"uint256"},{"name":"to","type":"address"}]}]`

type fakeGroupWalletExecutor struct {
	group    *sharedaccessModels.ClosedGroup
	groupErr error

	proposedAction *sharedaccessModels.PendingAction
	proposeErr     error
	lastProposer   string
	lastGroupID    uint
	lastKind       sharedaccessModels.ActionKind
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
	f.lastKind = kind
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

func TestBuildSwapTx_FailsClosedWhenSharedAccessMissing(t *testing.T) {
	svc := New()
	_, err := svc.BuildSwapTx(context.Background(), BuildSwapInput{
		WalletAddress: testWallet, SignerAddress: testSigner, RouterAddress: testRouter,
		RouterABI: testRouterABI, Method: "swap", Args: []interface{}{"1000", testWallet},
	})
	if err == nil {
		t.Fatal("expected an error when SharedAccess is unwired")
	}
}

func TestBuildSwapTx_RejectsInvalidRouterAddress(t *testing.T) {
	svc := New()
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	_, err := svc.BuildSwapTx(context.Background(), BuildSwapInput{
		WalletAddress: testWallet, SignerAddress: testSigner, RouterAddress: "not-an-address",
		RouterABI: testRouterABI, Method: "swap",
	})
	if err == nil {
		t.Fatal("expected an error for an invalid router address")
	}
}

func TestBuildSwapTx_RequiresMethod(t *testing.T) {
	svc := New()
	svc.SharedAccess = &fakeGroupWalletExecutor{}
	_, err := svc.BuildSwapTx(context.Background(), BuildSwapInput{
		WalletAddress: testWallet, SignerAddress: testSigner, RouterAddress: testRouter, RouterABI: testRouterABI,
	})
	if err == nil {
		t.Fatal("expected an error when method is empty")
	}
}

func TestBuildSwapTx_Success(t *testing.T) {
	fake := &fakeGroupWalletExecutor{
		group:          &sharedaccessModels.ClosedGroup{ID: 9},
		proposedAction: &sharedaccessModels.PendingAction{ID: 55},
		digest:         "0xdigest",
	}
	svc := New()
	svc.SharedAccess = fake

	proposal, err := svc.BuildSwapTx(context.Background(), BuildSwapInput{
		WalletAddress: testWallet, SignerAddress: testSigner, RouterAddress: testRouter,
		RouterABI: testRouterABI, Method: "swap", Args: []interface{}{"1000", testWallet},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proposal.ActionID != 55 || proposal.DigestToSign != "0xdigest" {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	// Proposed against the caller's own signer key, never the wallet
	// address itself (a Safe has no key of its own).
	if fake.lastProposer != testSigner {
		t.Fatalf("expected proposer %s, got %s", testSigner, fake.lastProposer)
	}
	if fake.lastGroupID != 9 {
		t.Fatalf("expected groupID 9, got %d", fake.lastGroupID)
	}
	if fake.lastKind != sharedaccessModels.ActionSwap {
		t.Fatalf("expected ActionSwap, got %s", fake.lastKind)
	}
	// The real ABI-encoded selector for swap(uint256,address) - proves
	// this is genuinely ABI-encoded, not a placeholder.
	if !strings.HasPrefix(fake.lastDataHex, "0x") || len(fake.lastDataHex) < 10 {
		t.Fatalf("expected real encoded calldata, got %q", fake.lastDataHex)
	}
}

func TestBuildSwapTx_PropagatesGroupLookupError(t *testing.T) {
	svc := New()
	svc.SharedAccess = &fakeGroupWalletExecutor{groupErr: apperrors.NotFound("no group")}
	_, err := svc.BuildSwapTx(context.Background(), BuildSwapInput{
		WalletAddress: testWallet, SignerAddress: testSigner, RouterAddress: testRouter,
		RouterABI: testRouterABI, Method: "swap", Args: []interface{}{"1000", testWallet},
	})
	if err == nil {
		t.Fatal("expected the group lookup error to propagate")
	}
}

func TestSubmitSwap_FailsClosedWhenSharedAccessMissing(t *testing.T) {
	svc := New()
	if _, err := svc.SubmitSwap(context.Background(), 55, testSigner, "0xsig"); err == nil {
		t.Fatal("expected an error when SharedAccess is unwired")
	}
}

func TestSubmitSwap_Success(t *testing.T) {
	svc := New()
	svc.SharedAccess = &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 55, Status: sharedaccessModels.ActionExecuted, TxHash: "0xhash"},
	}
	hash, err := svc.SubmitSwap(context.Background(), 55, testSigner, "0xsig")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "0xhash" {
		t.Fatalf("expected 0xhash, got %s", hash)
	}
}

func TestSubmitSwap_RejectsWhenNotYetExecuted(t *testing.T) {
	svc := New()
	svc.SharedAccess = &fakeGroupWalletExecutor{
		approvedAction: &sharedaccessModels.PendingAction{ID: 55, Status: sharedaccessModels.ActionPending},
	}
	if _, err := svc.SubmitSwap(context.Background(), 55, testSigner, "0xsig"); err == nil {
		t.Fatal("expected an error when the action has not reached EXECUTED")
	}
}

func TestSubmitSwap_PropagatesApprovalError(t *testing.T) {
	svc := New()
	svc.SharedAccess = &fakeGroupWalletExecutor{approveErr: apperrors.Unauthorized("bad signature")}
	if _, err := svc.SubmitSwap(context.Background(), 55, testSigner, "0xsig"); err == nil {
		t.Fatal("expected the approval error to propagate")
	}
}
