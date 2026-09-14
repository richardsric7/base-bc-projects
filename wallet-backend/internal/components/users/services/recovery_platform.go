// Wallet-recovery Branch B's platform infrastructure (PLAN.md §15.3,
// §15.9 Phases 1-2): the recovery service's own Safe - genuine internal
// N-of-M among the platform's trusted operators, never a single hot key
// - and the shared RecoveryGuard contract every enrolled primary wallet
// installs to restrict what that recovery-service owner slot may do.
// Both are deployed once, idempotently, at server boot
// (EnsureRecoveryPlatformDeployed, called from main.go) - not per user.
package services

import (
	"context"
	"crypto/ecdsa"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/users/models"
	"wallet-backend/internal/contracts"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/safe"
)

// deriveRecoveryPlatformDeployerKey derives the key that pays gas to
// deploy Branch B's platform infrastructure - a distinct role suffix
// from deriveDeployerKey's "primary-wallet-deployer" (same DeployerKeySalt,
// same "permissionless deployment call, no special authority" class of
// key, matching that salt's own doc comment on GlobalConfig, just a
// clearly separate audit trail from per-user wallet deployments).
func (s *Service) deriveRecoveryPlatformDeployerKey() (*ecdsa.PrivateKey, error) {
	return cryptoutil.DeriveKey(s.DeployerKeySalt + "|recovery-platform-deployer")
}

// recoveryOperatorKeys derives each platform trusted operator's key from
// its own configured salt - genuinely distinct secrets, not the same
// salt reused with different role suffixes, since an N-of-M scheme where
// every "independent" key traces back to one underlying secret provides
// no real protection against that secret's compromise.
func (s *Service) recoveryOperatorKeys() ([]*ecdsa.PrivateKey, error) {
	if len(s.RecoveryOperatorKeySalts) == 0 {
		return nil, apperrors.Internal("wallet recovery (Branch B) is not configured - set RECOVERY_OPERATOR_KEY_SALTS")
	}
	keys := make([]*ecdsa.PrivateKey, 0, len(s.RecoveryOperatorKeySalts))
	for _, salt := range s.RecoveryOperatorKeySalts {
		key, err := cryptoutil.DeriveKey(salt + "|recovery-operator")
		if err != nil {
			return nil, apperrors.Internal("failed to derive a recovery operator key")
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// RecoveryOperatorAddresses returns every platform trusted operator's
// address - the recovery-service Safe's own owners.
func (s *Service) RecoveryOperatorAddresses() ([]common.Address, error) {
	keys, err := s.recoveryOperatorKeys()
	if err != nil {
		return nil, err
	}
	addrs := make([]common.Address, len(keys))
	for i, key := range keys {
		addrs[i] = crypto.PubkeyToAddress(key.PublicKey)
	}
	return addrs, nil
}

// recoveryServiceThreshold returns the configured threshold, or a
// majority of operatorCount if none was explicitly configured (0).
func (s *Service) recoveryServiceThreshold(operatorCount int) *big.Int {
	if s.RecoveryServiceThreshold > 0 {
		return big.NewInt(int64(s.RecoveryServiceThreshold))
	}
	return big.NewInt(int64(operatorCount/2 + 1))
}

// recoveryServiceSafeSaltNonce never needs to vary, the same reasoning
// as users/services.go's primarySafeSaltNonce: Safe folds
// keccak256(initializer) into the actual CREATE2 salt, and there is only
// ever one recovery-service Safe for the whole platform, so there is
// nothing for a second, different saltNonce to disambiguate.
var recoveryServiceSafeSaltNonce = big.NewInt(0)

// ComputeRecoveryServiceSafeAddress deterministically derives the
// recovery-service Safe's address (owners = every configured operator,
// threshold = recoveryServiceThreshold) without requiring it to exist
// on-chain yet - the same computed-before-deployed pattern
// computePrimaryWalletAddress uses for a user's own primary wallet.
func (s *Service) ComputeRecoveryServiceSafeAddress() (common.Address, []byte, error) {
	operators, err := s.RecoveryOperatorAddresses()
	if err != nil {
		return common.Address{}, nil, err
	}
	initializer, err := safe.EncodeSetupCalldata(operators, s.recoveryServiceThreshold(len(operators)))
	if err != nil {
		return common.Address{}, nil, apperrors.Internal("failed to encode recovery service safe setup calldata")
	}
	return safe.ComputeProxyAddress(safe.SingletonAddress, initializer, recoveryServiceSafeSaltNonce), initializer, nil
}

// RecoveryPlatformState loads the singleton platform-infrastructure row,
// creating it (all zero values) on first use.
func (s *Service) RecoveryPlatformState() (*models.RecoveryPlatformInfrastructure, error) {
	var state models.RecoveryPlatformInfrastructure
	if err := s.DB.FirstOrCreate(&state, models.RecoveryPlatformInfrastructure{ID: 1}).Error; err != nil {
		return nil, apperrors.Internal("failed to load recovery platform state")
	}
	return &state, nil
}

// EnsureRecoveryPlatformDeployed idempotently deploys Branch B's
// platform infrastructure - the recovery-service Safe, then the
// RecoveryGuard contract naming it - exactly once, remembering success
// in RecoveryPlatformInfrastructure's singleton row so a later boot
// never redeploys. Called from main.go before the router starts serving
// traffic, the same "must run before enrollment can happen" placement as
// sharedaccessSvc.ReconcileRelayers. If RecoveryOperatorKeySalts is
// unconfigured, this is a deliberate no-op (logged, not fatal) - Branch B
// simply stays unavailable, same posture as every other optional vendor
// integration in this codebase (e.g. StablerailEnabled).
func (s *Service) EnsureRecoveryPlatformDeployed(ctx context.Context) error {
	if len(s.RecoveryOperatorKeySalts) == 0 {
		log.Printf("[users] wallet recovery (Branch B) is not configured (no RECOVERY_OPERATOR_KEY_SALTS) - skipping platform infrastructure deployment")
		return nil
	}

	state, err := s.RecoveryPlatformState()
	if err != nil {
		return err
	}

	recoveryServiceAddr, initializer, err := s.ComputeRecoveryServiceSafeAddress()
	if err != nil {
		return err
	}

	if state.RecoveryServiceAddress == "" {
		state.RecoveryServiceAddress = recoveryServiceAddr.Hex()
	} else if !strings.EqualFold(state.RecoveryServiceAddress, recoveryServiceAddr.Hex()) {
		log.Printf("[users] WARNING: RECOVERY_OPERATOR_KEY_SALTS/RECOVERY_SERVICE_THRESHOLD no longer match the already-deployed recovery-service Safe (deployed at %s, current config computes %s) - leaving the deployed Safe untouched; every already-enrolled wallet's second owner still refers to the deployed address, not the new computed one", state.RecoveryServiceAddress, recoveryServiceAddr.Hex())
	}

	deployerKey, err := s.deriveRecoveryPlatformDeployerKey()
	if err != nil {
		return apperrors.Internal("failed to derive the recovery platform deployer key")
	}

	if !state.RecoveryServiceDeployed {
		calldata, err := safe.EncodeCreateProxyWithNonceCalldata(safe.SingletonAddress, initializer, recoveryServiceSafeSaltNonce)
		if err != nil {
			return apperrors.Internal("failed to encode recovery service safe deployment calldata")
		}
		factoryAddr := safe.ProxyFactoryAddress
		if _, err := s.Blockchain.SignAndSubmitTx(ctx, deployerKey, &factoryAddr, big.NewInt(0), calldata, nil); err != nil {
			return apperrors.Internal("failed to deploy the recovery service safe: " + err.Error())
		}
		state.RecoveryServiceDeployed = true
		log.Printf("[users] deployed the recovery-service Safe at %s", state.RecoveryServiceAddress)
		// Persisted immediately, before attempting the Guard deployment
		// below: if that next step fails, a later retry must not
		// resubmit this already-succeeded deployment.
		if err := s.DB.Save(state).Error; err != nil {
			return apperrors.Internal("failed to persist recovery platform state")
		}
	}

	if !state.GuardDeployed {
		deployData, err := contracts.RecoveryGuardDeployData(state.RecoveryServiceAddress)
		if err != nil {
			return apperrors.Internal("failed to encode recovery guard deployment data")
		}
		contractAddr, _, err := s.Blockchain.DeployContract(ctx, deployerKey, deployData)
		if err != nil {
			return apperrors.Internal("failed to deploy the recovery guard: " + err.Error())
		}
		state.GuardAddress = contractAddr
		state.GuardDeployed = true
		log.Printf("[users] deployed the RecoveryGuard contract at %s", state.GuardAddress)
		if err := s.DB.Save(state).Error; err != nil {
			return apperrors.Internal("failed to persist recovery platform state")
		}
	}

	return nil
}
