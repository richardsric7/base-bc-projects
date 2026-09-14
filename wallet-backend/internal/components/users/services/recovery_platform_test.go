package services

import (
	"context"
	"testing"
)

func TestComputeRecoveryServiceSafeAddress_DeterministicAndDistinct(t *testing.T) {
	svc := newTestService(t)
	svc.RecoveryOperatorKeySalts = []string{"operator-salt-a", "operator-salt-b", "operator-salt-c"}
	svc.RecoveryServiceThreshold = 2

	addr1, _, err := svc.ComputeRecoveryServiceSafeAddress()
	if err != nil {
		t.Fatalf("ComputeRecoveryServiceSafeAddress: %v", err)
	}
	addr2, _, err := svc.ComputeRecoveryServiceSafeAddress()
	if err != nil {
		t.Fatalf("ComputeRecoveryServiceSafeAddress: %v", err)
	}
	if addr1 != addr2 {
		t.Fatalf("expected the same config to compute the same address, got %s and %s", addr1.Hex(), addr2.Hex())
	}

	svc2 := newTestService(t)
	svc2.RecoveryOperatorKeySalts = []string{"operator-salt-a", "operator-salt-b", "operator-salt-different"}
	svc2.RecoveryServiceThreshold = 2
	addr3, _, err := svc2.ComputeRecoveryServiceSafeAddress()
	if err != nil {
		t.Fatalf("ComputeRecoveryServiceSafeAddress: %v", err)
	}
	if addr1 == addr3 {
		t.Fatal("expected a different operator set to compute a different address")
	}
}

func TestComputeRecoveryServiceSafeAddress_RequiresOperatorSalts(t *testing.T) {
	svc := newTestService(t)
	if _, _, err := svc.ComputeRecoveryServiceSafeAddress(); err == nil {
		t.Fatal("expected an error when no operator salts are configured")
	}
}

func TestEnsureRecoveryPlatformDeployed_NoOpWhenUnconfigured(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc := New(newTestDB(t), nil, "test-recovery-authority-salt", 0, blockchain, "test-deployer-salt")

	if err := svc.EnsureRecoveryPlatformDeployed(context.Background()); err != nil {
		t.Fatalf("expected no error when Branch B is unconfigured, got %v", err)
	}
	if len(blockchain.submitted) != 0 {
		t.Fatalf("expected no on-chain submissions, got %d", len(blockchain.submitted))
	}
}

func TestEnsureRecoveryPlatformDeployed_DeploysOnceIdempotently(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc := New(newTestDB(t), nil, "test-recovery-authority-salt", 0, blockchain, "test-deployer-salt")
	svc.RecoveryOperatorKeySalts = []string{"operator-salt-a", "operator-salt-b", "operator-salt-c"}

	if err := svc.EnsureRecoveryPlatformDeployed(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := svc.EnsureRecoveryPlatformDeployed(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if len(blockchain.submitted) != 2 {
		t.Fatalf("expected exactly 2 on-chain submissions (Safe deploy + Guard deploy) across two calls, got %d", len(blockchain.submitted))
	}

	state, err := svc.RecoveryPlatformState()
	if err != nil {
		t.Fatalf("RecoveryPlatformState: %v", err)
	}
	if !state.RecoveryServiceDeployed || !state.GuardDeployed {
		t.Fatalf("expected both pieces of infrastructure to be marked deployed, got %+v", state)
	}
	if state.RecoveryServiceAddress == "" || state.GuardAddress == "" {
		t.Fatalf("expected both addresses to be recorded, got %+v", state)
	}
}

func TestEnsureRecoveryPlatformDeployed_PropagatesSafeDeploymentFailure(t *testing.T) {
	blockchain := &fakeBlockchain{err: errBoom}
	svc := New(newTestDB(t), nil, "test-recovery-authority-salt", 0, blockchain, "test-deployer-salt")
	svc.RecoveryOperatorKeySalts = []string{"operator-salt-a"}

	if err := svc.EnsureRecoveryPlatformDeployed(context.Background()); err == nil {
		t.Fatal("expected the recovery-service Safe deployment failure to propagate")
	}
}

func TestEnsureRecoveryPlatformDeployed_PropagatesGuardDeploymentFailure(t *testing.T) {
	blockchain := &fakeBlockchain{deployContractErr: errBoom}
	svc := New(newTestDB(t), nil, "test-recovery-authority-salt", 0, blockchain, "test-deployer-salt")
	svc.RecoveryOperatorKeySalts = []string{"operator-salt-a"}

	if err := svc.EnsureRecoveryPlatformDeployed(context.Background()); err == nil {
		t.Fatal("expected the RecoveryGuard deployment failure to propagate")
	}

	state, err := svc.RecoveryPlatformState()
	if err != nil {
		t.Fatalf("RecoveryPlatformState: %v", err)
	}
	if !state.RecoveryServiceDeployed {
		t.Fatal("expected the recovery-service Safe deployment to have already succeeded and been recorded")
	}
	if state.GuardDeployed {
		t.Fatal("expected the guard deployment to not be marked deployed after a failure")
	}
}
