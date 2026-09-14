// Package services holds the users component's business logic. Controllers
// stay thin (parse request -> call a Service method -> write response); all
// decisions live here so they're unit-testable without spinning up gin.
package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	referenceModels "wallet-backend/internal/components/reference/models"
	"wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/geoip"
	"wallet-backend/internal/notify"
	"wallet-backend/internal/validators"
)

type Service struct {
	DB                    *gorm.DB
	Mailer                notify.Mailer
	RecoveryAuthoritySalt string
	RecoveryOTPTTL        time.Duration
	// GeoIP resolves a registering caller's IP to a country code for the
	// risk fields below - defaults to geoip.NoopProvider (see New), so
	// registration behaves identically with or without one configured.
	GeoIP geoip.Provider
}

func New(db *gorm.DB, mailer notify.Mailer, recoveryAuthoritySalt string, recoveryOTPTTL time.Duration) *Service {
	return &Service{DB: db, Mailer: mailer, RecoveryAuthoritySalt: recoveryAuthoritySalt, RecoveryOTPTTL: recoveryOTPTTL, GeoIP: geoip.NewNoopProvider()}
}

// RegisterInput is the payload accepted by Register.
type RegisterInput struct {
	Username string
	Email    string
	Address  string
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

// Register creates a new user profile around an address that has already
// proven ownership via a signed request (middleware.SignatureAuth,
// PLAN.md §12) - the caller wires this to the address resolved from that
// verified request, never a value taken from the request body, so a user
// can never register a profile for an address they don't control. The
// server never generates or handles the matching private key.
func (s *Service) Register(input RegisterInput) (*models.User, error) {
	if !validators.IsValidUsername(input.Username) {
		return nil, apperrors.BadRequest("username must be 3-32 alphanumeric/underscore characters")
	}
	if !validators.IsValidEmail(input.Email) {
		return nil, apperrors.BadRequest("invalid email address")
	}
	if !validators.IsValidAddress(input.Address) {
		return nil, apperrors.BadRequest("invalid EVM address")
	}
	reserved, err := s.isReservedUsername(input.Username)
	if err != nil {
		return nil, err
	}
	if reserved {
		return nil, apperrors.Conflict("this username is reserved")
	}

	var existing models.User
	err = s.DB.Where("username = ? OR email = ? OR address = ?", input.Username, input.Email, input.Address).
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
		Address:                input.Address,
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
			Address:   input.Address,
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
