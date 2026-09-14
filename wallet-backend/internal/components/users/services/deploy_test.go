package services

import (
	"context"
	"testing"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/safe"
)

func TestDeployPrimaryWallet_SubmitsCreateProxyWithNonce(t *testing.T) {
	db := newTestDB(t)
	blockchain := &fakeBlockchain{}
	svc := New(db, nil, "test-recovery-salt", 0, blockchain, "test-deployer-salt")

	signerAddress := randomAddress(t)
	user, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	deployed, err := svc.DeployPrimaryWallet(context.Background(), signerAddress)
	if err != nil {
		t.Fatalf("DeployPrimaryWallet: %v", err)
	}
	if !deployed.PrimaryWalletDeployed {
		t.Fatal("expected PrimaryWalletDeployed to be true after a successful deployment")
	}

	if len(blockchain.submitted) != 1 {
		t.Fatalf("expected exactly one submitted transaction, got %d", len(blockchain.submitted))
	}
	if blockchain.submitted[0].to != safe.ProxyFactoryAddress {
		t.Fatalf("expected the deployment tx to target the SafeProxyFactory, got %s", blockchain.submitted[0].to.Hex())
	}

	reloaded, err := svc.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !reloaded.PrimaryWalletDeployed {
		t.Fatal("expected PrimaryWalletDeployed to be persisted")
	}
}

func TestDeployPrimaryWallet_IsIdempotent(t *testing.T) {
	db := newTestDB(t)
	blockchain := &fakeBlockchain{}
	svc := New(db, nil, "test-recovery-salt", 0, blockchain, "test-deployer-salt")

	signerAddress := randomAddress(t)
	if _, err := svc.Register(RegisterInput{Username: "bob", Email: "bob@example.com", SignerAddress: signerAddress}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := svc.DeployPrimaryWallet(context.Background(), signerAddress); err != nil {
		t.Fatalf("first DeployPrimaryWallet: %v", err)
	}
	if _, err := svc.DeployPrimaryWallet(context.Background(), signerAddress); err != nil {
		t.Fatalf("second DeployPrimaryWallet: %v", err)
	}

	if len(blockchain.submitted) != 1 {
		t.Fatalf("expected the deployment to be submitted exactly once across two calls, got %d", len(blockchain.submitted))
	}
}

func TestDeployPrimaryWallet_UnknownSigner(t *testing.T) {
	db := newTestDB(t)
	svc := New(db, nil, "test-recovery-salt", 0, &fakeBlockchain{}, "test-deployer-salt")

	_, err := svc.DeployPrimaryWallet(context.Background(), randomAddress(t))
	if err == nil {
		t.Fatal("expected an error for a signer with no registered user")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 404 {
		t.Fatalf("expected a 404 AppError, got %#v", err)
	}
}

func TestDeployPrimaryWallet_PropagatesSubmissionFailure(t *testing.T) {
	db := newTestDB(t)
	blockchain := &fakeBlockchain{err: errBoom}
	svc := New(db, nil, "test-recovery-salt", 0, blockchain, "test-deployer-salt")

	signerAddress := randomAddress(t)
	if _, err := svc.Register(RegisterInput{Username: "carol", Email: "carol@example.com", SignerAddress: signerAddress}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := svc.DeployPrimaryWallet(context.Background(), signerAddress); err == nil {
		t.Fatal("expected the deployment failure to propagate")
	}

	user, err := svc.GetByUsername("carol")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if user.PrimaryWalletDeployed {
		t.Fatal("PrimaryWalletDeployed must not be set when submission fails")
	}
}

// TestDeployPrimaryWallet_CreatesSharedAccessGroup verifies the fix for
// PLAN.md §13.9's flagged follow-up: without a ClosedGroup/GroupMember row
// matching the deployed Safe, payments/swaps would have no group to
// propose against and a primary wallet could never execute a real Safe
// transaction at all - a real, on-chain Safe orphaned from this
// application's own execution pipeline.
func TestDeployPrimaryWallet_CreatesSharedAccessGroup(t *testing.T) {
	db := newTestDB(t)
	svc := New(db, nil, "test-recovery-salt", 0, &fakeBlockchain{}, "test-deployer-salt")

	signerAddress := randomAddress(t)
	user, err := svc.Register(RegisterInput{Username: "dana", Email: "dana@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := svc.DeployPrimaryWallet(context.Background(), signerAddress); err != nil {
		t.Fatalf("DeployPrimaryWallet: %v", err)
	}

	var group sharedaccessModels.ClosedGroup
	if err := db.Where("address = ?", user.Address).First(&group).Error; err != nil {
		t.Fatalf("expected a ClosedGroup row for the deployed primary wallet: %v", err)
	}
	if group.Threshold != 1 {
		t.Fatalf("expected threshold 1, got %d", group.Threshold)
	}
	if group.Purpose != sharedaccessModels.PurposeWalletAccess {
		t.Fatalf("expected PurposeWalletAccess, got %s", group.Purpose)
	}

	var member sharedaccessModels.GroupMember
	if err := db.Where("group_id = ?", group.ID).First(&member).Error; err != nil {
		t.Fatalf("expected a GroupMember row: %v", err)
	}
	// The member is the signer EOA directly, not user.Address - the
	// primary wallet is the base case of the nested-EIP-1271 chain, not
	// itself nested (sharedaccess.resolveGroupOwnerSigner).
	if member.MemberAddress != signerAddress {
		t.Fatalf("expected member address %s, got %s", signerAddress, member.MemberAddress)
	}
	if member.Role != sharedaccessModels.RoleInitiatorApprover {
		t.Fatalf("expected INITIATOR_APPROVER, got %s", member.Role)
	}
}

// TestDeployPrimaryWallet_BackfillsGroupForAlreadyDeployedWallet covers a
// wallet deployed before this fix existed: PrimaryWalletDeployed is
// already true, so the deployment branch is skipped entirely, but the
// group must still be created (or the wallet stays permanently orphaned).
func TestDeployPrimaryWallet_BackfillsGroupForAlreadyDeployedWallet(t *testing.T) {
	db := newTestDB(t)
	svc := New(db, nil, "test-recovery-salt", 0, &fakeBlockchain{}, "test-deployer-salt")

	signerAddress := randomAddress(t)
	user, err := svc.Register(RegisterInput{Username: "erin", Email: "erin@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := db.Model(&user).Update("primary_wallet_deployed", true).Error; err != nil {
		t.Fatalf("simulate a pre-existing deployment: %v", err)
	}

	if _, err := svc.DeployPrimaryWallet(context.Background(), signerAddress); err != nil {
		t.Fatalf("DeployPrimaryWallet: %v", err)
	}

	var count int64
	if err := db.Model(&sharedaccessModels.ClosedGroup{}).Where("address = ?", user.Address).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one backfilled group, got %d", count)
	}
}
