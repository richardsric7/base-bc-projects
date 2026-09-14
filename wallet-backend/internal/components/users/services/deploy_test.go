package services

import (
	"context"
	"testing"

	"wallet-backend/internal/apperrors"
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
