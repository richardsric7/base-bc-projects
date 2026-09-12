// Package services holds the users component's business logic. Controllers
// stay thin (parse request -> call a Service method -> write response); all
// decisions live here so they're unit-testable without spinning up gin.
package services

import (
	"errors"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/validators"
)

type Service struct {
	DB *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{DB: db}
}

// RegisterInput is the payload accepted by Register.
type RegisterInput struct {
	Username string
	Email    string
	Address  string
}

// Register creates a new user profile around an address that has already
// proven ownership via SIWE (see internal/components/auth) - the caller
// wires this to the address from the caller's verified session, never a
// value taken from the request body, so a user can never register a
// profile for an address they don't control. The server never generates or
// handles the matching private key.
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

	var existing models.User
	err := s.DB.Where("username = ? OR email = ? OR address = ?", input.Username, input.Email, input.Address).
		First(&existing).Error
	if err == nil {
		return nil, apperrors.Conflict("username, email or address is already registered")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for existing user")
	}

	user := models.User{
		Username:  input.Username,
		Email:     input.Email,
		Address:   input.Address,
		KYCStatus: "pending",
	}

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

// VerifySecurityAnswer checks a candidate answer against the stored hash;
// used as one factor in the account-recovery flow (combine with an email
// OTP check in the controller before allowing any account change).
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
