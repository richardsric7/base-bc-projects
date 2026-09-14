// Package services holds the users component's business logic. Controllers
// stay thin (parse request -> call a Service method -> write response); all
// decisions live here so they're unit-testable without spinning up gin.
package services

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	referenceModels "wallet-backend/internal/components/reference/models"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/geoip"
	"wallet-backend/internal/network"
	"wallet-backend/internal/notify"
	"wallet-backend/internal/safe"
	"wallet-backend/internal/validators"
)

// BlockchainClient is the narrow slice of *network.Client this component
// needs: submitting the primary-wallet Safe deployment transaction (see
// DeployPrimaryWallet), and - for wallet-recovery Branch B (PLAN.md §15) -
// deploying the recovery-service Safe and RecoveryGuard contract as
// platform infrastructure, reading a Safe's nonce/owners to build a
// self-management transaction, waiting for that transaction's receipt,
// and building the plain native-ETH fee-payment transaction Branch B's
// enrollment fee uses. Narrowed to an interface, same pattern as every
// other component that talks to the chain, so it's unit-testable without
// a live Base RPC.
type BlockchainClient interface {
	SignAndSubmitTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
	DeployContract(ctx context.Context, deployer *ecdsa.PrivateKey, data []byte) (contractAddress, txHash string, err error)
	SafeNonce(ctx context.Context, safeAddress string) (*big.Int, error)
	SafeOwners(ctx context.Context, safeAddress string) ([]common.Address, error)
	WaitForReceipt(ctx context.Context, txHash string) (bool, error)
	BuildNativeTransferTx(ctx context.Context, from, to string, amountWei *big.Int, explicitNonce *uint64) (*network.UnsignedTx, error)
}

type Service struct {
	DB                    *gorm.DB
	Mailer                notify.Mailer
	RecoveryAuthoritySalt string
	RecoveryOTPTTL        time.Duration
	// GeoIP resolves a registering caller's IP to a country code for the
	// risk fields below - defaults to geoip.NoopProvider (see New), so
	// registration behaves identically with or without one configured.
	GeoIP geoip.Provider
	// Blockchain and DeployerKeySalt back DeployPrimaryWallet: Blockchain
	// submits the createProxyWithNonce call, and DeployerKeySalt seeds
	// the server-controlled key that pays its gas (cryptoutil.DeriveKey,
	// same derived-key pattern as every other server-controlled role in
	// this codebase - see fiat's activation faucet). Deploying a Safe via
	// its factory is a permissionless call that needs no authority over
	// the wallet itself, so a single platform-operated key covers every
	// user's deployment.
	Blockchain      BlockchainClient
	DeployerKeySalt string

	// RecoveryOperatorKeySalts/RecoveryServiceThreshold/
	// WalletRecoveryFeeWei configure wallet-recovery Branch B (PLAN.md
	// §15) - see recovery_platform.go and wallet_recovery.go. A nil/empty
	// RecoveryOperatorKeySalts leaves Branch B unusable (every Branch B
	// entry point returns an error rather than panicking) without
	// affecting Branch A (recovery.go) at all, so a deployment that
	// never configures these still boots and serves every other route
	// normally.
	RecoveryOperatorKeySalts []string
	RecoveryServiceThreshold int
	WalletRecoveryFeeWei     *big.Int

	// ChainID is needed to compute a Safe's EIP-712 domain separator
	// (safe.DomainSeparator) for Branch B's self-management transactions -
	// assigned post-construction in main.go, the same pattern as
	// sharedaccess's own Service.ChainID field.
	ChainID int64
}

func New(db *gorm.DB, mailer notify.Mailer, recoveryAuthoritySalt string, recoveryOTPTTL time.Duration, blockchain BlockchainClient, deployerKeySalt string) *Service {
	return &Service{
		DB:                    db,
		Mailer:                mailer,
		RecoveryAuthoritySalt: recoveryAuthoritySalt,
		RecoveryOTPTTL:        recoveryOTPTTL,
		GeoIP:                 geoip.NewNoopProvider(),
		Blockchain:            blockchain,
		DeployerKeySalt:       deployerKeySalt,
		WalletRecoveryFeeWei:  big.NewInt(0),
	}
}

// deriveDeployerKey derives the single server-controlled key that pays gas
// for deploying users' primary-wallet Safes (see DeployPrimaryWallet) -
// the same try-and-increment derivation used for every other
// server-controlled role in this codebase (cryptoutil.DeriveKey). An
// operator funds this one address with ETH ahead of time; there is no
// deployer private key at rest anywhere.
func (s *Service) deriveDeployerKey() (*ecdsa.PrivateKey, error) {
	return cryptoutil.DeriveKey(s.DeployerKeySalt + "|primary-wallet-deployer")
}

// primarySafeSaltNonce is the CREATE2 saltNonce every primary wallet Safe
// is deployed with. It never needs to vary between users: Safe folds
// keccak256(initializer) into the actual salt (see safe.ComputeProxyAddress),
// and each user's initializer already differs because it names a
// different sole owner (their SignerAddress) - reusing 0 here can never
// collide two different users onto the same computed address.
var primarySafeSaltNonce = big.NewInt(0)

// computePrimaryWalletAddress deterministically derives the Safe address
// that will control signerAddress's primary wallet - a single owner,
// threshold 1, CompatibilityFallbackHandlerAddress installed (see
// safe.EncodeSetupCalldata) - without requiring that Safe to exist
// on-chain yet (PLAN.md §13.11).
func computePrimaryWalletAddress(signerAddress common.Address) (common.Address, []byte, error) {
	initializer, err := safe.EncodeSetupCalldata([]common.Address{signerAddress}, big.NewInt(1))
	if err != nil {
		return common.Address{}, nil, apperrors.Internal("failed to encode primary wallet setup calldata")
	}
	return safe.ComputeProxyAddress(safe.SingletonAddress, initializer, primarySafeSaltNonce), initializer, nil
}

// RegisterInput is the payload accepted by Register.
type RegisterInput struct {
	Username string
	Email    string
	// SignerAddress is the EOA that proved ownership via a signed request
	// (middleware.SignatureAuth, PLAN.md §12) - Register computes the
	// user's actual primary wallet (a Safe owned solely by this key) from
	// it, rather than registering this address itself as the wallet (see
	// PLAN.md §13.2).
	SignerAddress string
	// CreatedByServiceLinkID is nil for a normal self-registration; a
	// servicelinks partner onboarding one of its own users sets it to
	// their own ServiceLink.ID (see internal/components/servicelinks).
	CreatedByServiceLinkID *uint
	// RegistrationIP is the caller's IP (controllers wire this to
	// c.ClientIP(), never a client-supplied header) - used only for the
	// best-effort geo-IP risk fields below, never to gate registration
	// itself.
	RegistrationIP string
}

// Register creates a new user profile around a signer EOA that has
// already proven ownership via a signed request (middleware.SignatureAuth,
// PLAN.md §12) - the caller wires SignerAddress to the address resolved
// from that verified request, never a value taken from the request body,
// so a user can never register a profile keyed on an EOA they don't
// control. The server never generates or handles the matching private
// key.
//
// The user's actual wallet identity (Address) is not SignerAddress
// itself: it's the Safe smart-contract account SignerAddress solely owns,
// computed deterministically via safe.ComputeProxyAddress (PLAN.md
// §13.2/§13.3) - permanent for the life of the account even if
// SignerAddress is later replaced via wallet recovery (PLAN.md §15.6).
// Registration deliberately never deploys that Safe on-chain (PLAN.md
// §13.11) - see DeployPrimaryWallet for that.
func (s *Service) Register(input RegisterInput) (*models.User, error) {
	if !validators.IsValidUsername(input.Username) {
		return nil, apperrors.BadRequest("username must be 3-32 alphanumeric/underscore characters")
	}
	if !validators.IsValidEmail(input.Email) {
		return nil, apperrors.BadRequest("invalid email address")
	}
	if !validators.IsValidAddress(input.SignerAddress) {
		return nil, apperrors.BadRequest("invalid EVM address")
	}
	reserved, err := s.isReservedUsername(input.Username)
	if err != nil {
		return nil, err
	}
	if reserved {
		return nil, apperrors.Conflict("this username is reserved")
	}

	primaryWalletAddress, _, err := computePrimaryWalletAddress(common.HexToAddress(input.SignerAddress))
	if err != nil {
		return nil, err
	}
	walletAddressHex := primaryWalletAddress.Hex()

	var existing models.User
	err = s.DB.Where("username = ? OR email = ? OR signer_address = ? OR address = ?", input.Username, input.Email, input.SignerAddress, walletAddressHex).
		First(&existing).Error
	if err == nil {
		return nil, apperrors.Conflict("username, email or address is already registered")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for existing user")
	}

	user := models.User{
		Username:               input.Username,
		Email:                  input.Email,
		Address:                walletAddressHex,
		SignerAddress:          input.SignerAddress,
		KYCStatus:              "pending",
		CreatedByServiceLinkID: input.CreatedByServiceLinkID,
	}
	s.applyRegistrationRiskFields(&user, input.RegistrationIP)

	txErr := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		wallet := models.UserWallet{
			UserID:    user.ID,
			Address:   walletAddressHex,
			Label:     "primary",
			IsPrimary: true,
		}
		return tx.Create(&wallet).Error
	})
	if txErr != nil {
		return nil, apperrors.Internal("failed to create user")
	}

	return &user, nil
}

// DeployPrimaryWallet submits the on-chain createProxyWithNonce call that
// actually deploys callerSignerAddress's primary wallet Safe at the
// address Register already computed and stored - a no-op success if it's
// already deployed, so calling this more than once (a retried client
// request, a duplicate activation webhook) never double-submits. Gas is
// paid by the platform's own deriveDeployerKey, not the user's own
// balance: deploying a Safe via its factory is a permissionless call that
// grants the caller no authority over the resulting wallet.
//
// This is the one on-chain activation step PLAN.md §13.11 requires before
// a primary wallet can do anything beyond passively receiving funds:
// fund a sub-wallet from it, enable shared access on it, or be named as
// another wallet's owner all call a Safe method, which needs code at the
// address to call into.
func (s *Service) DeployPrimaryWallet(ctx context.Context, callerSignerAddress string) (*models.User, error) {
	if !validators.IsValidAddress(callerSignerAddress) {
		return nil, apperrors.BadRequest("invalid EVM address")
	}
	var user models.User
	if err := s.DB.Where("signer_address = ?", callerSignerAddress).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user profile not found - register first")
		}
		return nil, apperrors.Internal("failed to load user")
	}
	if user.PrimaryWalletDeployed {
		if err := s.ensurePrimaryWalletGroup(&user); err != nil {
			return nil, err
		}
		return &user, nil
	}

	_, initializer, err := computePrimaryWalletAddress(common.HexToAddress(user.SignerAddress))
	if err != nil {
		return nil, err
	}
	calldata, err := safe.EncodeCreateProxyWithNonceCalldata(safe.SingletonAddress, initializer, primarySafeSaltNonce)
	if err != nil {
		return nil, apperrors.Internal("failed to encode primary wallet deployment calldata")
	}
	deployerKey, err := s.deriveDeployerKey()
	if err != nil {
		return nil, apperrors.Internal("failed to derive the primary wallet deployer key")
	}
	factoryAddr := safe.ProxyFactoryAddress
	if _, err := s.Blockchain.SignAndSubmitTx(ctx, deployerKey, &factoryAddr, big.NewInt(0), calldata, nil); err != nil {
		return nil, apperrors.Internal("failed to deploy primary wallet: " + err.Error())
	}

	if err := s.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("primary_wallet_deployed", true).Error; err != nil {
		return nil, apperrors.Internal("failed to record primary wallet deployment")
	}
	user.PrimaryWalletDeployed = true

	if err := s.ensurePrimaryWalletGroup(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

// ensurePrimaryWalletGroup idempotently creates the sharedaccess
// ClosedGroup/GroupMember rows the primary wallet's own on-chain Safe
// needs to actually be usable: PLAN.md §13's own design says "every
// wallet, the primary included, is a ClosedGroup row" (owners:
// [User.SignerAddress], threshold: 1), but Register/DeployPrimaryWallet
// never actually wrote one - only internal/components/users' own
// UserWallet index row. Without it, payments/swaps (PLAN.md §13.9's
// flagged follow-up) have no group to propose against, and a primary
// wallet can never actually execute a real Safe transaction - it's a
// genuine Safe deployed on-chain (owners/threshold correctly set at
// deployment) but orphaned from this application's own execution
// pipeline. GroupMember.MemberAddress is the signer EOA directly (not
// User.Address) since the primary wallet is the base case of the
// nested-EIP-1271 chain, not itself nested - see
// sharedaccess.resolveGroupOwnerSigner.
func (s *Service) ensurePrimaryWalletGroup(user *models.User) error {
	var existing sharedaccessModels.ClosedGroup
	err := s.DB.Where("address = ?", user.Address).First(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.Internal("failed to check for an existing primary wallet group")
	}

	addressHex := user.Address
	group := sharedaccessModels.ClosedGroup{
		Name:      "Primary wallet",
		Purpose:   sharedaccessModels.PurposeWalletAccess,
		Address:   &addressHex,
		Threshold: 1,
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&group).Error; err != nil {
			return apperrors.Internal("failed to record primary wallet group: " + err.Error())
		}
		member := sharedaccessModels.GroupMember{
			GroupID:       group.ID,
			MemberAddress: user.SignerAddress,
			Role:          sharedaccessModels.RoleInitiatorApprover,
		}
		if err := tx.Create(&member).Error; err != nil {
			return apperrors.Internal("failed to record primary wallet group membership: " + err.Error())
		}
		return nil
	})
}

// applyRegistrationRiskFields resolves registrationIP to a country code
// via s.GeoIP and, if that country has a reference.CountryConfig row
// marked HighRisk, sets RegistrationHighRisk - purely advisory fields for
// downstream review, so any failure here (no provider configured, lookup
// error, unknown IP) is swallowed rather than propagated: it must never
// block registration itself (PLAN.md §4.13).
func (s *Service) applyRegistrationRiskFields(user *models.User, registrationIP string) {
	countryCode, err := s.GeoIP.Lookup(context.Background(), registrationIP)
	if err != nil || countryCode == "" {
		return
	}
	user.RegistrationCountryCode = countryCode

	var config referenceModels.CountryConfig
	if err := s.DB.Where("country_code = ?", countryCode).First(&config).Error; err == nil {
		user.RegistrationHighRisk = config.HighRisk
	}
}

// GetByUsername fetches a single user's public profile.
func (s *Service) GetByUsername(username string) (*models.User, error) {
	var user models.User
	if err := s.DB.Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user not found")
		}
		return nil, apperrors.Internal("failed to load user")
	}
	return &user, nil
}

// GetByAddress fetches a user profile by their EVM address, used to check
// whether the address behind a verified session has completed profile
// registration yet.
func (s *Service) GetByAddress(address string) (*models.User, error) {
	var user models.User
	if err := s.DB.Where("address = ?", address).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user not found")
		}
		return nil, apperrors.Internal("failed to load user")
	}
	return &user, nil
}

// GetByID fetches a user profile by primary key.
func (s *Service) GetByID(userID uint) (*models.User, error) {
	var user models.User
	if err := s.DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user not found")
		}
		return nil, apperrors.Internal("failed to load user")
	}
	return &user, nil
}

// RegisterWallet adds an additional EVM address a user controls (e.g. a
// hardware-wallet address, or a sub-wallet a servicelinks partner has the
// user provision) alongside their primary wallet.
func (s *Service) RegisterWallet(userID uint, address, label string) (*models.UserWallet, error) {
	if !validators.IsValidAddress(address) {
		return nil, apperrors.BadRequest("invalid EVM address")
	}
	wallet := models.UserWallet{UserID: userID, Address: address, Label: label}
	if err := s.DB.Create(&wallet).Error; err != nil {
		return nil, apperrors.Conflict("this address is already registered")
	}
	return &wallet, nil
}

// SetKYCVerifiedLevel directly sets a user's KYC verification level -
// used by internal/components/kyc's own vendor webhook flow and by
// internal/components/servicelinks' partner-driven KYC-status override
// (see PLAN.md §4.11 finding 6 for the ownership check callers must apply
// before calling this - this method itself trusts the caller entirely).
func (s *Service) SetKYCVerifiedLevel(userID uint, level int) error {
	if err := s.DB.Model(&models.User{}).Where("id = ?", userID).Update("kyc_verified_level", level).Error; err != nil {
		return apperrors.Internal("failed to update KYC status")
	}
	return nil
}

// Delete removes a user account. Only the account owner (verified by the
// SIWE-issued session JWT) may call this for their own username.
func (s *Service) Delete(username string) error {
	res := s.DB.Where("username = ?", username).Delete(&models.User{})
	if res.Error != nil {
		return apperrors.Internal("failed to delete user")
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("user not found")
	}
	return nil
}

// ListSecurityQuestions returns the fixed catalog of recovery questions.
func (s *Service) ListSecurityQuestions() ([]models.SecurityQuestion, error) {
	var questions []models.SecurityQuestion
	if err := s.DB.Find(&questions).Error; err != nil {
		return nil, apperrors.Internal("failed to load security questions")
	}
	return questions, nil
}

// SetSecurityAnswer hashes and stores (or replaces) a user's answer to one
// security question, used later for account recovery.
func (s *Service) SetSecurityAnswer(userID, questionID uint, answer string) error {
	hash, err := cryptoutil.HashPassword(answer)
	if err != nil {
		return apperrors.Internal("failed to store security answer")
	}

	var existing models.UserSecurityAnswer
	err = s.DB.Where("user_id = ? AND security_question_id = ?", userID, questionID).First(&existing).Error
	if err == nil {
		existing.AnswerHash = hash
		if err := s.DB.Save(&existing).Error; err != nil {
			return apperrors.Internal("failed to update security answer")
		}
		return nil
	}

	record := models.UserSecurityAnswer{UserID: userID, SecurityQuestionID: questionID, AnswerHash: hash}
	if err := s.DB.Create(&record).Error; err != nil {
		return apperrors.Internal("failed to store security answer")
	}
	return nil
}

// VerifySecurityAnswer checks a candidate answer against the stored hash -
// an authenticated "check my own answer" utility for a logged-in user. The
// actual account-recovery flow (for someone who can no longer sign in at
// all) is unauthenticated and lives in recovery.go's Recover, which
// verifies every configured answer plus an email OTP together.
func (s *Service) VerifySecurityAnswer(userID, questionID uint, answer string) (bool, error) {
	var record models.UserSecurityAnswer
	err := s.DB.Where("user_id = ? AND security_question_id = ?", userID, questionID).First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, apperrors.NotFound("no security answer on file for this question")
		}
		return false, apperrors.Internal("failed to load security answer")
	}
	return cryptoutil.CheckPasswordHash(answer, record.AnswerHash), nil
}

// isReservedUsername reports whether username (case-insensitively) matches
// a reserved name - staff handles, brand names, impersonation-prone
// strings - that no one may register.
func (s *Service) isReservedUsername(username string) (bool, error) {
	var count int64
	err := s.DB.Model(&models.ReservedName{}).Where("name = ?", strings.ToLower(username)).Count(&count).Error
	if err != nil {
		return false, apperrors.Internal("failed to check reserved usernames")
	}
	return count > 0, nil
}

// EnableAccountRecovery opts a user into recovery-by-security-question so a
// future loss of wallet access can be recovered from - see recovery.go.
func (s *Service) EnableAccountRecovery(userID uint) error {
	if err := s.DB.Model(&models.User{}).Where("id = ?", userID).Update("account_recovery_enabled", true).Error; err != nil {
		return apperrors.Internal("failed to enable account recovery")
	}
	return nil
}

// DisableAccountRecovery opts a user out.
func (s *Service) DisableAccountRecovery(userID uint) error {
	if err := s.DB.Model(&models.User{}).Where("id = ?", userID).Update("account_recovery_enabled", false).Error; err != nil {
		return apperrors.Internal("failed to disable account recovery")
	}
	return nil
}
