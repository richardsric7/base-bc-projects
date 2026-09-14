package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"wallet-backend/internal/components/users/models"
)

// newWalletRecoveryTestService builds a Service with Branch B configured
// (three distinct operator salts, a threshold of 2) against a fresh
// in-memory DB and the given fake blockchain.
func newWalletRecoveryTestService(t *testing.T, blockchain *fakeBlockchain) (*Service, *capturingMailer) {
	t.Helper()
	mailer := &capturingMailer{}
	svc := New(newTestDB(t), mailer, "test-recovery-authority-salt", 15*time.Minute, blockchain, "test-deployer-salt")
	svc.RecoveryOperatorKeySalts = []string{"operator-salt-a", "operator-salt-b", "operator-salt-c"}
	svc.RecoveryServiceThreshold = 2
	svc.ChainID = 84532
	return svc, mailer
}

func deployRecoveryPlatform(t *testing.T, svc *Service) {
	t.Helper()
	if err := svc.EnsureRecoveryPlatformDeployed(context.Background()); err != nil {
		t.Fatalf("EnsureRecoveryPlatformDeployed: %v", err)
	}
}

// registerDeployedUser registers a fresh user and marks their primary
// wallet deployed (Branch B refuses to operate on an undeployed Safe),
// returning both the user and the private key behind their SignerAddress
// so tests can produce real personal_sign signatures.
func registerDeployedUser(t *testing.T, svc *Service, username string) (*models.User, *ecdsa.PrivateKey) {
	t.Helper()
	signerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate signer key: %v", err)
	}
	signerAddress := crypto.PubkeyToAddress(signerKey.PublicKey).Hex()
	user, err := svc.Register(RegisterInput{Username: username, Email: username + "@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := svc.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("primary_wallet_deployed", true).Error; err != nil {
		t.Fatalf("mark primary wallet deployed: %v", err)
	}
	user.PrimaryWalletDeployed = true
	return user, signerKey
}

// signDigestHex personal_sign's the 32-byte digest a Build*Challenge
// hands back (accounts.TextHash over its raw bytes - the same hashing
// cryptoutil.VerifyPersonalSignBytes performs on the verifying side) and
// returns the 0x-prefixed hex signature a controller would receive from
// a real wallet.
func signDigestHex(t *testing.T, digestHex string, key *ecdsa.PrivateKey) string {
	t.Helper()
	digest := common.HexToHash(digestHex)
	hash := accounts.TextHash(digest.Bytes())
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign digest: %v", err)
	}
	sig[64] += 27
	return "0x" + common.Bytes2Hex(sig)
}

func TestBuildEnableWalletRecovery_RequiresDeployedPrimaryWallet(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)

	signerAddress := randomAddress(t)
	if _, err := svc.Register(RegisterInput{Username: "carol", Email: "carol@example.com", SignerAddress: signerAddress}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.BuildEnableWalletRecovery(context.Background(), signerAddress); err == nil {
		t.Fatal("expected an error when the primary wallet is not yet deployed")
	}
}

func TestBuildEnableWalletRecovery_RequiresPlatformDeployed(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	// Deliberately not calling deployRecoveryPlatform.
	user, _ := registerDeployedUser(t, svc, "dave")

	if _, err := svc.BuildEnableWalletRecovery(context.Background(), user.SignerAddress); err == nil {
		t.Fatal("expected an error when platform infrastructure has not finished deploying")
	}
}

func TestEnableWalletRecovery_EndToEnd_Success(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)
	user, signerKey := registerDeployedUser(t, svc, "alice")

	challenge, err := svc.BuildEnableWalletRecovery(context.Background(), user.SignerAddress)
	if err != nil {
		t.Fatalf("BuildEnableWalletRecovery: %v", err)
	}
	if challenge.AddOwnerSafeTxHash == "" || challenge.SetGuardSafeTxHash == "" {
		t.Fatalf("expected both challenge hashes to be populated, got %+v", challenge)
	}
	if challenge.FeeTx != nil {
		t.Fatal("expected no fee tx when WalletRecoveryFeeWei is unset")
	}

	addOwnerSig := signDigestHex(t, challenge.AddOwnerSafeTxHash, signerKey)
	setGuardSig := signDigestHex(t, challenge.SetGuardSafeTxHash, signerKey)

	updated, err := svc.ConfirmEnableWalletRecovery(context.Background(), user.SignerAddress, "", addOwnerSig, setGuardSig)
	if err != nil {
		t.Fatalf("ConfirmEnableWalletRecovery: %v", err)
	}
	if !updated.WalletRecoveryEnabled {
		t.Fatal("expected WalletRecoveryEnabled to be true")
	}

	reloaded, err := svc.GetByID(user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !reloaded.WalletRecoveryEnabled {
		t.Fatal("expected WalletRecoveryEnabled to be persisted")
	}
}

func TestConfirmEnableWalletRecovery_RejectsWrongSignature(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)
	user, _ := registerDeployedUser(t, svc, "bob")

	challenge, err := svc.BuildEnableWalletRecovery(context.Background(), user.SignerAddress)
	if err != nil {
		t.Fatalf("BuildEnableWalletRecovery: %v", err)
	}

	wrongKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}
	addOwnerSig := signDigestHex(t, challenge.AddOwnerSafeTxHash, wrongKey)
	setGuardSig := signDigestHex(t, challenge.SetGuardSafeTxHash, wrongKey)

	if _, err := svc.ConfirmEnableWalletRecovery(context.Background(), user.SignerAddress, "", addOwnerSig, setGuardSig); err == nil {
		t.Fatal("expected an error for a signature from a key other than the wallet's current signer")
	}
}

func TestConfirmEnableWalletRecovery_RequiresFeeTxHashWhenFeeConfigured(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	svc.WalletRecoveryFeeWei = big.NewInt(1_000_000_000_000_000)
	deployRecoveryPlatform(t, svc)
	user, signerKey := registerDeployedUser(t, svc, "erin")

	challenge, err := svc.BuildEnableWalletRecovery(context.Background(), user.SignerAddress)
	if err != nil {
		t.Fatalf("BuildEnableWalletRecovery: %v", err)
	}
	if challenge.FeeTx == nil {
		t.Fatal("expected a fee tx to be built when WalletRecoveryFeeWei is set")
	}

	addOwnerSig := signDigestHex(t, challenge.AddOwnerSafeTxHash, signerKey)
	setGuardSig := signDigestHex(t, challenge.SetGuardSafeTxHash, signerKey)

	if _, err := svc.ConfirmEnableWalletRecovery(context.Background(), user.SignerAddress, "", addOwnerSig, setGuardSig); err == nil {
		t.Fatal("expected an error when feeTxHash is required but missing")
	}
	if _, err := svc.ConfirmEnableWalletRecovery(context.Background(), user.SignerAddress, "0xfeetxhash", addOwnerSig, setGuardSig); err != nil {
		t.Fatalf("expected success once feeTxHash is supplied: %v", err)
	}
}

func TestDisableWalletRecovery_EndToEnd_Success(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)
	user, signerKey := registerDeployedUser(t, svc, "frank")

	enableChallenge, err := svc.BuildEnableWalletRecovery(context.Background(), user.SignerAddress)
	if err != nil {
		t.Fatalf("BuildEnableWalletRecovery: %v", err)
	}
	addOwnerSig := signDigestHex(t, enableChallenge.AddOwnerSafeTxHash, signerKey)
	setGuardSig := signDigestHex(t, enableChallenge.SetGuardSafeTxHash, signerKey)
	if _, err := svc.ConfirmEnableWalletRecovery(context.Background(), user.SignerAddress, "", addOwnerSig, setGuardSig); err != nil {
		t.Fatalf("ConfirmEnableWalletRecovery: %v", err)
	}

	state, err := svc.RecoveryPlatformState()
	if err != nil {
		t.Fatalf("RecoveryPlatformState: %v", err)
	}
	// The fake blockchain doesn't actually track owner-set changes on its
	// own, so tell it what SafeOwners should now report - exactly what a
	// real chain would show after the enable transactions above landed.
	blockchain.safeOwners = []common.Address{common.HexToAddress(user.SignerAddress), common.HexToAddress(state.RecoveryServiceAddress)}

	disableChallenge, err := svc.BuildDisableWalletRecovery(context.Background(), user.SignerAddress)
	if err != nil {
		t.Fatalf("BuildDisableWalletRecovery: %v", err)
	}
	removeSig := signDigestHex(t, disableChallenge.RemoveOwnerSafeTxHash, signerKey)
	clearGuardSig := signDigestHex(t, disableChallenge.ClearGuardSafeTxHash, signerKey)

	updated, err := svc.ConfirmDisableWalletRecovery(context.Background(), user.SignerAddress, removeSig, clearGuardSig)
	if err != nil {
		t.Fatalf("ConfirmDisableWalletRecovery: %v", err)
	}
	if updated.WalletRecoveryEnabled {
		t.Fatal("expected WalletRecoveryEnabled to be false after disabling")
	}
}

func TestBuildDisableWalletRecovery_RejectsWhenNotEnabled(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)
	user, _ := registerDeployedUser(t, svc, "gina")

	if _, err := svc.BuildDisableWalletRecovery(context.Background(), user.SignerAddress); err == nil {
		t.Fatal("expected an error when wallet recovery was never enabled")
	}
}

// registerWithWalletRecoveryEnabled sets up a user identical to what a
// successful ConfirmEnableWalletRecovery would leave behind (security
// questions answered, WalletRecoveryEnabled set), without going through
// the full enable flow - RecoverWallet's own tests care about the
// recovery-execution logic, not re-proving enrollment already covered
// above.
func registerWithWalletRecoveryEnabled(t *testing.T, svc *Service, username string) (*models.User, *ecdsa.PrivateKey, []SecurityAnswerInput) {
	t.Helper()
	user, signerKey := registerDeployedUser(t, svc, username)

	questions := []models.SecurityQuestion{
		{Question: "What was the name of your first pet?"},
		{Question: "What city were you born in?"},
	}
	if err := svc.DB.Create(&questions).Error; err != nil {
		t.Fatalf("create questions: %v", err)
	}
	answers := []SecurityAnswerInput{
		{SecurityQuestionID: questions[0].ID, Answer: "Rex"},
		{SecurityQuestionID: questions[1].ID, Answer: "Lagos"},
	}
	for _, a := range answers {
		if err := svc.SetSecurityAnswer(user.ID, a.SecurityQuestionID, a.Answer); err != nil {
			t.Fatalf("SetSecurityAnswer: %v", err)
		}
	}
	if err := svc.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("wallet_recovery_enabled", true).Error; err != nil {
		t.Fatalf("enable wallet recovery: %v", err)
	}
	user.WalletRecoveryEnabled = true
	return user, signerKey, answers
}

func TestRecoverWallet_Success_PreservesAddressAndSwapsSigner(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, mailer := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)
	user, signerKey, answers := registerWithWalletRecoveryEnabled(t, svc, "grace")
	blockchain.safeOwners = []common.Address{crypto.PubkeyToAddress(signerKey.PublicKey)}

	if err := svc.RequestRecoveryOTP("grace"); err != nil {
		t.Fatalf("RequestRecoveryOTP: %v", err)
	}
	otp := mailer.extractOTP(t)

	newSignerAddress, newSignerSig := newRecoveryAddress(t, "grace")
	logEntry, err := svc.RecoverWallet("grace", newSignerAddress, newSignerSig, otp, answers)
	if err != nil {
		t.Fatalf("RecoverWallet: %v", err)
	}
	if logEntry.OldSignerAddress != user.SignerAddress {
		t.Fatalf("expected OldSignerAddress %s, got %s", user.SignerAddress, logEntry.OldSignerAddress)
	}
	if logEntry.NewSignerAddress != newSignerAddress {
		t.Fatalf("expected NewSignerAddress %s, got %s", newSignerAddress, logEntry.NewSignerAddress)
	}
	if logEntry.WalletAddress != user.Address {
		t.Fatalf("expected the wallet address to be recorded unchanged, got %s want %s", logEntry.WalletAddress, user.Address)
	}
	if logEntry.AuthoritySignature == "" {
		t.Fatal("expected a tamper-evident authority signature to be recorded")
	}

	reloaded, err := svc.GetByUsername("grace")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if reloaded.Address != user.Address {
		t.Fatalf("Branch B must never change the wallet's Address, got %s want %s", reloaded.Address, user.Address)
	}
	if reloaded.SignerAddress != newSignerAddress {
		t.Fatalf("expected SignerAddress to be updated to %s, got %s", newSignerAddress, reloaded.SignerAddress)
	}

	// Single-use: the OTP must be consumed, matching Branch A's Recover.
	if _, err := svc.RecoverWallet("grace", newSignerAddress, newSignerSig, otp, answers); err == nil {
		t.Fatal("expected a second RecoverWallet with the same OTP to fail")
	}
}

func TestRecoverWallet_RejectsWhenNotEnabled(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, _ := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)
	user, _ := registerDeployedUser(t, svc, "henry")

	newSignerAddress, newSignerSig := newRecoveryAddress(t, "henry")
	if _, err := svc.RecoverWallet("henry", newSignerAddress, newSignerSig, "000000", nil); err == nil {
		t.Fatalf("expected an error when wallet recovery is not enabled for %s", user.Username)
	}
}

func TestRecoverWallet_RejectsWrongSecurityAnswers(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, mailer := newWalletRecoveryTestService(t, blockchain)
	deployRecoveryPlatform(t, svc)
	_, _, answers := registerWithWalletRecoveryEnabled(t, svc, "ivan")
	blockchain.safeOwners = []common.Address{}

	if err := svc.RequestRecoveryOTP("ivan"); err != nil {
		t.Fatalf("RequestRecoveryOTP: %v", err)
	}
	otp := mailer.extractOTP(t)

	wrongAnswers := make([]SecurityAnswerInput, len(answers))
	copy(wrongAnswers, answers)
	wrongAnswers[0].Answer = "not the right answer"

	newSignerAddress, newSignerSig := newRecoveryAddress(t, "ivan")
	if _, err := svc.RecoverWallet("ivan", newSignerAddress, newSignerSig, otp, wrongAnswers); err == nil {
		t.Fatal("expected an error for wrong security answers")
	}
}
