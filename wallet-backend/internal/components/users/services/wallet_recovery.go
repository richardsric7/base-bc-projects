// Wallet-recovery Branch B (PLAN.md §15): true wallet-signer recovery via
// a Safe owner swap on the primary wallet, preserving its address, funds,
// sub-wallets and shared-access memberships automatically - a distinct,
// additive mechanism alongside recovery.go's Branch A (a free, DB-only
// address swap), never replacing it. Enrollment (enable/disable) adds or
// removes the recovery-service Safe as a second owner of the caller's own
// primary wallet, gated by §12's normal self-service model (the caller
// must already be that wallet's current signer) - the caller signs both
// self-management transactions themselves, this package only submits
// them (paying gas via the platform's own deployer key, since
// execTransaction is as permissionless to submit as any other Safe call
// this codebase already relays). RecoverWallet, by contrast, is the one
// deliberate exception to §12 this whole mechanism exists for (§15.6):
// the caller controls no currently-authorized key at all, so identity is
// proven via the shared security-question/OTP/new-key-personal_sign front
// door (reused as-is from recovery.go), and the authorizing Safe
// signature comes entirely from the platform's own recovery-service
// operator keys - a nested EIP-1271 contract signature, exactly PLAN.md
// §13.4's design for one Safe owning another.
package services

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
	"wallet-backend/internal/safe"
	"wallet-backend/internal/validators"
)

// walletRecoveryFeeWalletAddress is Branch B's one-off enrollment fee
// destination (PLAN.md §15.7) - a cryptoutil.DeriveKey-derived address,
// same pattern as every other server-controlled fee-collection role in
// this port (e.g. patron's FeeWalletAddress), derived from
// RecoveryAuthoritySalt with its own role suffix rather than a new salt,
// since that salt is already this component's namespace for
// recovery-related server-controlled roles.
func (s *Service) walletRecoveryFeeWalletAddress() (string, error) {
	key, err := cryptoutil.DeriveKey(s.RecoveryAuthoritySalt + "|wallet-recovery-fee-wallet")
	if err != nil {
		return "", apperrors.Internal("failed to derive the wallet recovery fee wallet address")
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), nil
}

// requireDeployedUserBySigner loads the user currently controlling
// signerAddress and requires their primary wallet Safe to already be
// deployed - every Branch B operation calls a real Safe method, which
// needs code at the address to call into (same requirement
// DeployPrimaryWallet's own doc comment states for §13.11 in general).
func (s *Service) requireDeployedUserBySigner(signerAddress string) (*models.User, error) {
	if !validators.IsValidAddress(signerAddress) {
		return nil, apperrors.BadRequest("invalid EVM address")
	}
	var user models.User
	if err := s.DB.Where("signer_address = ?", signerAddress).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user profile not found")
		}
		return nil, apperrors.Internal("failed to load user")
	}
	if !user.PrimaryWalletDeployed {
		return nil, apperrors.Conflict("the primary wallet must be deployed before using wallet recovery")
	}
	return &user, nil
}

// requireRecoveryPlatformDeployed loads Branch B's platform
// infrastructure state and requires both pieces to have finished
// deploying - see EnsureRecoveryPlatformDeployed in recovery_platform.go.
func (s *Service) requireRecoveryPlatformDeployed() (*models.RecoveryPlatformInfrastructure, error) {
	state, err := s.RecoveryPlatformState()
	if err != nil {
		return nil, err
	}
	if !state.RecoveryServiceDeployed || !state.GuardDeployed {
		return nil, apperrors.Conflict("wallet recovery is not available yet - platform infrastructure has not finished deploying")
	}
	return state, nil
}

// verifySingleOwnerSignature checks sigHex is a personal_sign signature
// of digest by owner - the same verification cryptoutil.
// VerifyPersonalSignBytes performs for every other Safe-transaction
// approval in this codebase (see sharedaccess's ApproveAction), reused
// here for Branch B's own self-management transactions.
func verifySingleOwnerSignature(digest common.Hash, sigHex string, owner common.Address) error {
	if !strings.HasPrefix(sigHex, "0x") {
		return apperrors.BadRequest("signature must be 0x-prefixed hex")
	}
	valid, err := cryptoutil.VerifyPersonalSignBytes(digest.Bytes(), common.FromHex(sigHex), owner)
	if err != nil {
		return apperrors.BadRequest("invalid signature: " + err.Error())
	}
	if !valid {
		return apperrors.Unauthorized("signature does not match the wallet's current signer")
	}
	return nil
}

// submitSelfManagementTx packs owner's single signature over tx,
// encodes and submits the resulting execTransaction call (gas paid by
// deployerKey, a platform-operated key - submitting a call with valid
// signatures already attached grants the submitter no authority over the
// Safe, the same permissionless-relay reasoning DeployPrimaryWallet's own
// doc comment gives for factory deployments), and waits for it to
// confirm before returning.
func (s *Service) submitSelfManagementTx(ctx context.Context, deployerKey *ecdsa.PrivateKey, safeAddr common.Address, tx safe.SafeTx, owner common.Address, sigHex string) error {
	sig, err := safe.EOAPersonalSignSignature(owner, common.FromHex(sigHex))
	if err != nil {
		return apperrors.BadRequest("malformed signature: " + err.Error())
	}
	packed, err := safe.PackSignatures([]safe.Signature{sig})
	if err != nil {
		return apperrors.Internal("failed to pack signature: " + err.Error())
	}
	calldata, err := safe.EncodeExecTransactionCalldata(tx, packed)
	if err != nil {
		return apperrors.Internal("failed to encode execution calldata")
	}
	txHash, err := s.Blockchain.SignAndSubmitTx(ctx, deployerKey, &safeAddr, big.NewInt(0), calldata, nil)
	if err != nil {
		return fmt.Errorf("submit transaction: %w", err)
	}
	ok, err := s.Blockchain.WaitForReceipt(ctx, txHash)
	if err != nil {
		return fmt.Errorf("await receipt for %s: %w", txHash, err)
	}
	if !ok {
		return fmt.Errorf("transaction %s reverted", txHash)
	}
	return nil
}

// EnableWalletRecoveryChallenge is what the caller must personal_sign
// (both SafeTxHash fields) - and, if a fee is configured, sign and
// submit FeeTx - before calling ConfirmEnableWalletRecovery.
type EnableWalletRecoveryChallenge struct {
	AddOwnerSafeTxHash string              `json:"addOwnerSafeTxHash"`
	SetGuardSafeTxHash string              `json:"setGuardSafeTxHash"`
	FeeTx              *network.UnsignedTx `json:"feeTx,omitempty"`
	FeeWalletAddress   string              `json:"feeWalletAddress,omitempty"`
	FeeAmountWei       string              `json:"feeAmountWei,omitempty"`
}

// enableWalletRecoveryTxs rebuilds the exact two SafeTx values Build/
// ConfirmEnableWalletRecovery both need, reading the Safe's nonce fresh
// each call - Build and Confirm are expected to happen back-to-back in
// one enrollment flow, so nonce drift between them is unlikely, and if it
// does happen the mismatch is caught as an ordinary signature-verification
// failure at Confirm time (the client must re-Build), the same small,
// accepted race this component's simpler self-service flows take rather
// than the full row-locked reservation PLAN.md §13.12 built for
// concurrent multi-approver group actions - overkill for a single user's
// own one-off enrollment.
func (s *Service) enableWalletRecoveryTxs(ctx context.Context, user *models.User, state *models.RecoveryPlatformInfrastructure) (addOwnerTx, setGuardTx safe.SafeTx, domainSeparator common.Hash, err error) {
	safeAddr := common.HexToAddress(user.Address)
	nonce, err := s.Blockchain.SafeNonce(ctx, user.Address)
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Internal("failed to read the primary wallet's current nonce: " + err.Error())
	}
	addOwnerData, err := safe.EncodeAddOwnerWithThresholdCalldata(common.HexToAddress(state.RecoveryServiceAddress), big.NewInt(1))
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Internal("failed to encode addOwnerWithThreshold calldata")
	}
	setGuardData, err := safe.EncodeSetGuardCalldata(common.HexToAddress(state.GuardAddress))
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Internal("failed to encode setGuard calldata")
	}
	domainSeparator = safe.DomainSeparator(big.NewInt(s.ChainID), safeAddr)
	addOwnerTx = safe.SafeTx{To: safeAddr, Data: addOwnerData, Nonce: nonce}
	setGuardTx = safe.SafeTx{To: safeAddr, Data: setGuardData, Nonce: new(big.Int).Add(nonce, big.NewInt(1))}
	return addOwnerTx, setGuardTx, domainSeparator, nil
}

// BuildEnableWalletRecovery returns the two SafeTxHashes the caller must
// personal_sign (with their current signer key) to enroll in Branch B -
// adding the recovery-service Safe as a second owner of their primary
// wallet, then installing RecoveryGuard - plus, if
// GlobalConfig.WalletRecoveryFeeWei is non-zero, an unsigned fee-payment
// transaction. callerSignerAddress must already be verified by the
// caller (middleware.SignatureAuth) - this fits §12's ordinary
// self-service model, unlike RecoverWallet below.
func (s *Service) BuildEnableWalletRecovery(ctx context.Context, callerSignerAddress string) (*EnableWalletRecoveryChallenge, error) {
	user, err := s.requireDeployedUserBySigner(callerSignerAddress)
	if err != nil {
		return nil, err
	}
	if user.WalletRecoveryEnabled {
		return nil, apperrors.Conflict("wallet recovery is already enabled for this account")
	}
	state, err := s.requireRecoveryPlatformDeployed()
	if err != nil {
		return nil, err
	}

	addOwnerTx, setGuardTx, domainSeparator, err := s.enableWalletRecoveryTxs(ctx, user, state)
	if err != nil {
		return nil, err
	}

	challenge := &EnableWalletRecoveryChallenge{
		AddOwnerSafeTxHash: safe.SafeTxHash(domainSeparator, addOwnerTx).Hex(),
		SetGuardSafeTxHash: safe.SafeTxHash(domainSeparator, setGuardTx).Hex(),
	}

	if s.WalletRecoveryFeeWei != nil && s.WalletRecoveryFeeWei.Sign() > 0 {
		feeWallet, err := s.walletRecoveryFeeWalletAddress()
		if err != nil {
			return nil, err
		}
		feeTx, err := s.Blockchain.BuildNativeTransferTx(ctx, user.SignerAddress, feeWallet, s.WalletRecoveryFeeWei, nil)
		if err != nil {
			return nil, apperrors.Internal("failed to build the enrollment fee transaction: " + err.Error())
		}
		challenge.FeeTx = feeTx
		challenge.FeeWalletAddress = feeWallet
		challenge.FeeAmountWei = s.WalletRecoveryFeeWei.String()
	}
	return challenge, nil
}

// ConfirmEnableWalletRecovery verifies the caller's two signatures over
// BuildEnableWalletRecovery's challenge, submits both self-management
// transactions in order (add-owner, then set-guard - the guard would
// otherwise briefly restrict the add-owner call itself, since it targets
// the Safe's own address with an owner-management selector but is signed
// by the wallet's own owner, not the recovery service, so it is in fact
// unrestricted either way; ordering is still add-then-guard so the guard
// is only ever installed once the second owner it references already
// exists), and marks the account enrolled. feeTxHash is trusted once
// supplied, matching this codebase's established posture for every other
// self-submitted on-chain payment (see patron's ConfirmSubscription).
func (s *Service) ConfirmEnableWalletRecovery(ctx context.Context, callerSignerAddress, feeTxHash, addOwnerSignatureHex, setGuardSignatureHex string) (*models.User, error) {
	user, err := s.requireDeployedUserBySigner(callerSignerAddress)
	if err != nil {
		return nil, err
	}
	if user.WalletRecoveryEnabled {
		return nil, apperrors.Conflict("wallet recovery is already enabled for this account")
	}
	state, err := s.requireRecoveryPlatformDeployed()
	if err != nil {
		return nil, err
	}
	if s.WalletRecoveryFeeWei != nil && s.WalletRecoveryFeeWei.Sign() > 0 && feeTxHash == "" {
		return nil, apperrors.BadRequest("feeTxHash is required to enable wallet recovery")
	}

	addOwnerTx, setGuardTx, domainSeparator, err := s.enableWalletRecoveryTxs(ctx, user, state)
	if err != nil {
		return nil, err
	}
	signerAddr := common.HexToAddress(user.SignerAddress)
	if err := verifySingleOwnerSignature(safe.SafeTxHash(domainSeparator, addOwnerTx), addOwnerSignatureHex, signerAddr); err != nil {
		return nil, err
	}
	if err := verifySingleOwnerSignature(safe.SafeTxHash(domainSeparator, setGuardTx), setGuardSignatureHex, signerAddr); err != nil {
		return nil, err
	}

	deployerKey, err := s.deriveRecoveryPlatformDeployerKey()
	if err != nil {
		return nil, apperrors.Internal("failed to derive the recovery platform deployer key")
	}
	safeAddr := common.HexToAddress(user.Address)
	if err := s.submitSelfManagementTx(ctx, deployerKey, safeAddr, addOwnerTx, signerAddr, addOwnerSignatureHex); err != nil {
		return nil, apperrors.Internal("failed to add the recovery service as an owner: " + err.Error())
	}
	if err := s.submitSelfManagementTx(ctx, deployerKey, safeAddr, setGuardTx, signerAddr, setGuardSignatureHex); err != nil {
		return nil, apperrors.Internal("failed to install the recovery guard: " + err.Error())
	}

	if err := s.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("wallet_recovery_enabled", true).Error; err != nil {
		return nil, apperrors.Internal("failed to record wallet recovery enrollment")
	}
	user.WalletRecoveryEnabled = true
	return user, nil
}

// DisableWalletRecoveryChallenge mirrors EnableWalletRecoveryChallenge
// for the reverse operation - no fee to pay to disable.
type DisableWalletRecoveryChallenge struct {
	RemoveOwnerSafeTxHash string `json:"removeOwnerSafeTxHash"`
	ClearGuardSafeTxHash  string `json:"clearGuardSafeTxHash"`
}

func (s *Service) disableWalletRecoveryTxs(ctx context.Context, user *models.User, state *models.RecoveryPlatformInfrastructure) (removeOwnerTx, clearGuardTx safe.SafeTx, domainSeparator common.Hash, err error) {
	safeAddr := common.HexToAddress(user.Address)
	recoveryServiceAddr := common.HexToAddress(state.RecoveryServiceAddress)

	owners, err := s.Blockchain.SafeOwners(ctx, user.Address)
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Internal("failed to read the primary wallet's current owners: " + err.Error())
	}
	prevOwner, err := safe.FindPrevOwner(owners, recoveryServiceAddr)
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Conflict("the recovery service is not currently an owner of this wallet")
	}
	nonce, err := s.Blockchain.SafeNonce(ctx, user.Address)
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Internal("failed to read the primary wallet's current nonce: " + err.Error())
	}
	removeData, err := safe.EncodeRemoveOwnerCalldata(prevOwner, recoveryServiceAddr, big.NewInt(1))
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Internal("failed to encode removeOwner calldata")
	}
	clearGuardData, err := safe.EncodeSetGuardCalldata(common.Address{})
	if err != nil {
		return safe.SafeTx{}, safe.SafeTx{}, common.Hash{}, apperrors.Internal("failed to encode setGuard calldata")
	}
	domainSeparator = safe.DomainSeparator(big.NewInt(s.ChainID), safeAddr)
	removeOwnerTx = safe.SafeTx{To: safeAddr, Data: removeData, Nonce: nonce}
	clearGuardTx = safe.SafeTx{To: safeAddr, Data: clearGuardData, Nonce: new(big.Int).Add(nonce, big.NewInt(1))}
	return removeOwnerTx, clearGuardTx, domainSeparator, nil
}

// BuildDisableWalletRecovery returns the two SafeTxHashes the caller
// must personal_sign to leave Branch B - removing the recovery service
// as an owner and clearing the guard, the exact reverse of enrollment.
func (s *Service) BuildDisableWalletRecovery(ctx context.Context, callerSignerAddress string) (*DisableWalletRecoveryChallenge, error) {
	user, err := s.requireDeployedUserBySigner(callerSignerAddress)
	if err != nil {
		return nil, err
	}
	if !user.WalletRecoveryEnabled {
		return nil, apperrors.Conflict("wallet recovery is not enabled for this account")
	}
	state, err := s.requireRecoveryPlatformDeployed()
	if err != nil {
		return nil, err
	}
	removeOwnerTx, clearGuardTx, domainSeparator, err := s.disableWalletRecoveryTxs(ctx, user, state)
	if err != nil {
		return nil, err
	}
	return &DisableWalletRecoveryChallenge{
		RemoveOwnerSafeTxHash: safe.SafeTxHash(domainSeparator, removeOwnerTx).Hex(),
		ClearGuardSafeTxHash:  safe.SafeTxHash(domainSeparator, clearGuardTx).Hex(),
	}, nil
}

// ConfirmDisableWalletRecovery verifies both signatures, submits both
// transactions, and marks the account no longer enrolled.
func (s *Service) ConfirmDisableWalletRecovery(ctx context.Context, callerSignerAddress, removeOwnerSignatureHex, clearGuardSignatureHex string) (*models.User, error) {
	user, err := s.requireDeployedUserBySigner(callerSignerAddress)
	if err != nil {
		return nil, err
	}
	if !user.WalletRecoveryEnabled {
		return nil, apperrors.Conflict("wallet recovery is not enabled for this account")
	}
	state, err := s.requireRecoveryPlatformDeployed()
	if err != nil {
		return nil, err
	}
	removeOwnerTx, clearGuardTx, domainSeparator, err := s.disableWalletRecoveryTxs(ctx, user, state)
	if err != nil {
		return nil, err
	}
	signerAddr := common.HexToAddress(user.SignerAddress)
	if err := verifySingleOwnerSignature(safe.SafeTxHash(domainSeparator, removeOwnerTx), removeOwnerSignatureHex, signerAddr); err != nil {
		return nil, err
	}
	if err := verifySingleOwnerSignature(safe.SafeTxHash(domainSeparator, clearGuardTx), clearGuardSignatureHex, signerAddr); err != nil {
		return nil, err
	}

	deployerKey, err := s.deriveRecoveryPlatformDeployerKey()
	if err != nil {
		return nil, apperrors.Internal("failed to derive the recovery platform deployer key")
	}
	safeAddr := common.HexToAddress(user.Address)
	if err := s.submitSelfManagementTx(ctx, deployerKey, safeAddr, removeOwnerTx, signerAddr, removeOwnerSignatureHex); err != nil {
		return nil, apperrors.Internal("failed to remove the recovery service owner: " + err.Error())
	}
	if err := s.submitSelfManagementTx(ctx, deployerKey, safeAddr, clearGuardTx, signerAddr, clearGuardSignatureHex); err != nil {
		return nil, apperrors.Internal("failed to clear the recovery guard: " + err.Error())
	}

	if err := s.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("wallet_recovery_enabled", false).Error; err != nil {
		return nil, apperrors.Internal("failed to record wallet recovery disablement")
	}
	user.WalletRecoveryEnabled = false
	return user, nil
}

// signAsRecoveryService produces the recovery-service Safe's own nested
// EIP-1271 contract-signature payload for outerPreImage (the primary
// wallet's own EncodeTransactionData pre-image, per PLAN.md §13.4's
// nested-ownership design) - personal_sign'd by enough of the platform's
// own trusted-operator keys to meet the recovery-service Safe's
// configured threshold. Unlike a GroupMember's approval (a human signs
// over HTTP), every one of these keys is itself server-derived
// (cryptoutil.DeriveKey), so RecoverWallet can produce this signature
// synchronously in one call with no external round trip.
func (s *Service) signAsRecoveryService(outerPreImage []byte, recoveryServiceAddr common.Address) ([]byte, error) {
	operatorKeys, err := s.recoveryOperatorKeys()
	if err != nil {
		return nil, err
	}
	operatorAddrs, err := s.RecoveryOperatorAddresses()
	if err != nil {
		return nil, err
	}
	threshold := int(s.recoveryServiceThreshold(len(operatorKeys)).Int64())
	if threshold > len(operatorKeys) {
		threshold = len(operatorKeys)
	}

	innerDomainSeparator := safe.DomainSeparator(big.NewInt(s.ChainID), recoveryServiceAddr)
	digest := safe.MessageHashForSafe(innerDomainSeparator, outerPreImage)
	messageHash := accounts.TextHash(digest.Bytes())

	sigs := make([]safe.Signature, 0, threshold)
	for i := 0; i < threshold; i++ {
		rawSig, err := crypto.Sign(messageHash, operatorKeys[i])
		if err != nil {
			return nil, apperrors.Internal("failed to sign as a recovery operator")
		}
		rawSig[64] += 27
		sig, err := safe.EOAPersonalSignSignature(operatorAddrs[i], rawSig)
		if err != nil {
			return nil, apperrors.Internal("failed to build a recovery operator signature")
		}
		sigs = append(sigs, sig)
	}
	return safe.PackSignatures(sigs)
}

// buildWalletRecoveryLog signs a description of this exact wallet
// recovery with the same recovery-authority key buildRecoveryLog (Branch
// A) uses, just describing a signer swap rather than an address change -
// PLAN.md §15.8's recommendation to reuse the same tamper-evident
// attestation pattern for Branch B's own audit trail.
func (s *Service) buildWalletRecoveryLog(username, walletAddress, oldSignerAddress, newSignerAddress, txHash string) (*models.WalletRecoveryLog, error) {
	authorityKey, err := cryptoutil.DeriveKey(s.RecoveryAuthoritySalt + "|wallet-recovery|" + username)
	if err != nil {
		return nil, apperrors.Internal("failed to derive the recovery authority key")
	}
	message := fmt.Sprintf("wallet-backend wallet recovery\nusername: %s\nwallet: %s\nold signer: %s\nnew signer: %s\ntx: %s", username, walletAddress, oldSignerAddress, newSignerAddress, txHash)
	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, authorityKey)
	if err != nil {
		return nil, apperrors.Internal("failed to sign the wallet recovery attestation")
	}
	sig[64] += 27
	signatureHex := "0x" + common.Bytes2Hex(sig)

	return &models.WalletRecoveryLog{
		Username:           username,
		WalletAddress:      walletAddress,
		OldSignerAddress:   oldSignerAddress,
		NewSignerAddress:   newSignerAddress,
		TxHash:             txHash,
		AuthoritySignature: signatureHex,
	}, nil
}

// RecoverWallet is Branch B's execution flow (PLAN.md §15.5): identity is
// proven exactly like Branch A's Recover (every configured security
// answer, a valid unexpired email OTP, and a personal_sign proof from the
// new signer key - all reused, called code, never duplicated), but
// instead of re-pointing the username to a new address, this submits a
// Safe swapOwner(prevOwner, oldSigner, newSigner) call against the
// primary wallet's own Safe: User.Address never changes, only
// User.SignerAddress does, in step with the Safe's actual on-chain
// owner. Unlike Recover, no GroupMember rows need revoking - every
// shared-access/sub-wallet reference names this wallet's Address, which
// never moved, so they all keep working automatically (PLAN.md §15.5
// step 3).
func (s *Service) RecoverWallet(username, newSignerAddress, newSignerAddressSignature, otp string, answers []SecurityAnswerInput) (*models.WalletRecoveryLog, error) {
	if !validators.IsValidAddress(newSignerAddress) {
		return nil, apperrors.BadRequest("invalid EVM address")
	}
	if err := verifyNewAddressOwnership(username, newSignerAddress, newSignerAddressSignature); err != nil {
		return nil, err
	}

	user, err := s.GetByUsername(username)
	if err != nil {
		return nil, err
	}
	if !user.WalletRecoveryEnabled {
		return nil, apperrors.Forbidden("wallet recovery is not enabled for this account")
	}
	if err := s.verifyAllSecurityAnswers(user.ID, answers); err != nil {
		return nil, err
	}
	verification, err := s.consumeValidOTP(user.ID, otp)
	if err != nil {
		return nil, err
	}

	state, err := s.requireRecoveryPlatformDeployed()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	safeAddr := common.HexToAddress(user.Address)
	oldSigner := common.HexToAddress(user.SignerAddress)
	newSigner := common.HexToAddress(newSignerAddress)
	recoveryServiceAddr := common.HexToAddress(state.RecoveryServiceAddress)

	owners, err := s.Blockchain.SafeOwners(ctx, user.Address)
	if err != nil {
		return nil, apperrors.Internal("failed to read the primary wallet's current owners: " + err.Error())
	}
	prevOwner, err := safe.FindPrevOwner(owners, oldSigner)
	if err != nil {
		return nil, apperrors.Internal("the lost signer is no longer an owner of this wallet: " + err.Error())
	}
	nonce, err := s.Blockchain.SafeNonce(ctx, user.Address)
	if err != nil {
		return nil, apperrors.Internal("failed to read the primary wallet's current nonce: " + err.Error())
	}

	swapData, err := safe.EncodeSwapOwnerCalldata(prevOwner, oldSigner, newSigner)
	if err != nil {
		return nil, apperrors.Internal("failed to encode swapOwner calldata")
	}
	tx := safe.SafeTx{To: safeAddr, Data: swapData, Nonce: nonce}
	outerDomainSeparator := safe.DomainSeparator(big.NewInt(s.ChainID), safeAddr)
	outerPreImage := safe.EncodeTransactionData(outerDomainSeparator, tx)

	innerPacked, err := s.signAsRecoveryService(outerPreImage, recoveryServiceAddr)
	if err != nil {
		return nil, err
	}
	packed, err := safe.PackSignatures([]safe.Signature{safe.ContractSignature(recoveryServiceAddr, innerPacked)})
	if err != nil {
		return nil, apperrors.Internal("failed to pack the recovery signature: " + err.Error())
	}
	calldata, err := safe.EncodeExecTransactionCalldata(tx, packed)
	if err != nil {
		return nil, apperrors.Internal("failed to encode execution calldata")
	}

	deployerKey, err := s.deriveRecoveryPlatformDeployerKey()
	if err != nil {
		return nil, apperrors.Internal("failed to derive the recovery platform deployer key")
	}
	txHash, err := s.Blockchain.SignAndSubmitTx(ctx, deployerKey, &safeAddr, big.NewInt(0), calldata, nil)
	if err != nil {
		return nil, apperrors.Internal("failed to submit the wallet recovery transaction: " + err.Error())
	}
	ok, err := s.Blockchain.WaitForReceipt(ctx, txHash)
	if err != nil {
		return nil, apperrors.Internal("failed to confirm the wallet recovery transaction: " + err.Error())
	}
	if !ok {
		return nil, apperrors.Internal("the wallet recovery transaction reverted on-chain")
	}

	logEntry, err := s.buildWalletRecoveryLog(username, user.Address, user.SignerAddress, newSignerAddress, txHash)
	if err != nil {
		return nil, err
	}

	txErr := s.DB.Transaction(func(dbTx *gorm.DB) error {
		if err := dbTx.Model(&models.User{}).Where("id = ?", user.ID).Update("signer_address", newSignerAddress).Error; err != nil {
			return err
		}
		if err := dbTx.Delete(verification).Error; err != nil {
			return err
		}
		return dbTx.Create(logEntry).Error
	})
	if txErr != nil {
		return nil, apperrors.Internal("wallet recovery executed on-chain but failed to record locally: " + txErr.Error())
	}
	return logEntry, nil
}
