// Account recovery is deliberately unauthenticated: its entire purpose is
// helping someone who can no longer produce a normal SIWE signature (lost
// device, lost key) regain control of their username. Two factors stand in
// for that missing signature - every configured security answer, and a
// one-time code emailed to the account's registered address - and a
// successful recovery re-points the username to a new, caller-supplied
// address rather than trying to restore the old one (which is impossible
// on Base: unlike a Stellar account, an EVM address *is* its key, so there
// is no "re-key the same address" operation to perform - see PLAN.md §4.3).
package services

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/validators"
)

// RequestRecoveryOTP emails a one-time recovery code to username's
// registered address, if recovery is enabled for that account. This always
// succeeds from the caller's point of view regardless of whether username
// exists or has recovery enabled, so the endpoint can't be used to
// enumerate registered usernames or which accounts opted into recovery.
func (s *Service) RequestRecoveryOTP(username string) error {
	var user models.User
	err := s.DB.Where("username = ?", username).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return apperrors.Internal("failed to look up user")
	}
	if !user.AccountRecoveryEnabled {
		return nil
	}

	code, err := generateOTP()
	if err != nil {
		return apperrors.Internal("failed to generate a recovery code")
	}

	record := models.AccountRecoveryEmailVerification{
		UserID:    user.ID,
		Code:      cryptoutil.HashSHA256Hex(code),
		ExpiresAt: time.Now().Add(s.RecoveryOTPTTL),
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return apperrors.Internal("failed to store the recovery code")
	}

	subject := "Your account recovery code"
	body := fmt.Sprintf("Your account recovery code is %s. It expires in %d minutes. If you didn't request this, ignore this email.", code, int(s.RecoveryOTPTTL.Minutes()))
	if err := s.Mailer.Send(user.Email, subject, body); err != nil {
		return apperrors.Internal("failed to send the recovery email")
	}
	return nil
}

func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// SecurityAnswerInput is one answer supplied during recovery.
type SecurityAnswerInput struct {
	SecurityQuestionID uint
	Answer             string
}

// RecoveryMessage is the exact text newAddress must sign (via personal_sign)
// to prove the caller controls its private key - see Recover. Exported so
// clients can construct it without guessing the format.
func RecoveryMessage(username, newAddress string) string {
	return fmt.Sprintf("wallet-backend account recovery\nusername: %s\nnew address: %s", username, newAddress)
}

// Recover verifies three factors - every configured security answer, a
// valid unexpired email OTP, and a personal_sign signature over
// RecoveryMessage from newAddress itself - and, if all check out, re-points
// username to newAddress. That third factor matters even though the other
// two already gate the request: it's the same non-custodial guarantee
// Register enforces (the server only ever attaches a username to an
// address whose key the caller has just demonstrated), so recovery can't
// silently attach an account to an address that was merely typed in, e.g.
// a fat-fingered or otherwise uncontrolled address. Any shared-access group
// memberships the old address held are revoked rather than transferred
// automatically - matching the original's security posture of requiring
// other group members to manually re-invite a recovered account once
// they're satisfied the recovery is legitimate, rather than silently
// trusting it.
func (s *Service) Recover(username, newAddress, newAddressSignature, otp string, answers []SecurityAnswerInput) (*models.AccountRecoveryLog, error) {
	if !validators.IsValidAddress(newAddress) {
		return nil, apperrors.BadRequest("invalid EVM address")
	}
	if err := verifyNewAddressOwnership(username, newAddress, newAddressSignature); err != nil {
		return nil, err
	}

	user, err := s.GetByUsername(username)
	if err != nil {
		return nil, err
	}
	if !user.AccountRecoveryEnabled {
		return nil, apperrors.Forbidden("account recovery is not enabled for this account")
	}

	if err := s.verifyAllSecurityAnswers(user.ID, answers); err != nil {
		return nil, err
	}

	verification, err := s.consumeValidOTP(user.ID, otp)
	if err != nil {
		return nil, err
	}

	oldAddress := user.Address
	logEntry, err := s.buildRecoveryLog(username, oldAddress, newAddress)
	if err != nil {
		return nil, err
	}

	txErr := s.DB.Transaction(func(tx *gorm.DB) error {
		// Branch A is a fresh identity, nothing preserved (PLAN.md §15,
		// table row 1) - newAddress becomes both Address and
		// SignerAddress, i.e. a bare, undeployed EOA-as-wallet identity
		// exactly like every account had before §13's Safe redesign,
		// deliberately not the safe.ComputeProxyAddress(newAddress) a
		// fresh Register call would compute. Setting only Address here
		// would leave SignerAddress pointing at the lost key forever,
		// silently reintroducing the very identity middleware.
		// SignatureAuth's self-service check relies on - see Branch B
		// (§15.5-§15.6) for the alternative that actually preserves the
		// old Safe, its funds, and its sub-wallets via a real owner swap.
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
			"address":        newAddress,
			"signer_address": newAddress,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.UserWallet{}).Where("user_id = ? AND is_primary = ?", user.ID, true).Update("address", newAddress).Error; err != nil {
			return err
		}
		if err := tx.Delete(verification).Error; err != nil {
			return err
		}
		if err := tx.Where("member_address = ?", oldAddress).Delete(&sharedaccessModels.GroupMember{}).Error; err != nil {
			return err
		}
		return tx.Create(logEntry).Error
	})
	if txErr != nil {
		return nil, apperrors.Internal("failed to complete account recovery")
	}
	return logEntry, nil
}

func verifyNewAddressOwnership(username, newAddress, signatureHex string) error {
	if !strings.HasPrefix(signatureHex, "0x") {
		return apperrors.BadRequest("newAddressSignature must be 0x-prefixed")
	}
	sigBytes := common.FromHex(signatureHex)
	valid, err := cryptoutil.VerifyPersonalSign(RecoveryMessage(username, newAddress), sigBytes, common.HexToAddress(newAddress))
	if err != nil || !valid {
		return apperrors.Unauthorized("newAddressSignature does not match the new address")
	}
	return nil
}

func (s *Service) verifyAllSecurityAnswers(userID uint, answers []SecurityAnswerInput) error {
	var configured []models.UserSecurityAnswer
	if err := s.DB.Where("user_id = ?", userID).Find(&configured).Error; err != nil {
		return apperrors.Internal("failed to load security answers")
	}
	if len(configured) == 0 {
		return apperrors.Conflict("no security questions are configured for this account")
	}
	if len(answers) != len(configured) {
		return apperrors.BadRequest(fmt.Sprintf("an answer is required for all %d configured security questions", len(configured)))
	}

	hashByQuestion := make(map[uint]string, len(configured))
	for _, a := range configured {
		hashByQuestion[a.SecurityQuestionID] = a.AnswerHash
	}
	for _, given := range answers {
		hash, ok := hashByQuestion[given.SecurityQuestionID]
		if !ok || !cryptoutil.CheckPasswordHash(given.Answer, hash) {
			return apperrors.Unauthorized("one or more security answers are incorrect")
		}
	}
	return nil
}

func (s *Service) consumeValidOTP(userID uint, otp string) (*models.AccountRecoveryEmailVerification, error) {
	var verification models.AccountRecoveryEmailVerification
	err := s.DB.Where("user_id = ? AND code = ?", userID, cryptoutil.HashSHA256Hex(otp)).
		Order("created_at DESC").First(&verification).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.Unauthorized("invalid recovery code")
		}
		return nil, apperrors.Internal("failed to verify the recovery code")
	}
	if time.Now().After(verification.ExpiresAt) {
		return nil, apperrors.Unauthorized("recovery code has expired")
	}
	return &verification, nil
}

// buildRecoveryLog signs a description of this exact recovery with a
// recovery-authority key derived via cryptoutil.DeriveKey - a tamper-evident
// attestation that the server's recovery authority (not just an
// application-layer database write) approved this specific change, in
// place of the on-chain co-signature the original's native Stellar
// multi-sig recovery used.
func (s *Service) buildRecoveryLog(username, oldAddress, newAddress string) (*models.AccountRecoveryLog, error) {
	authorityKey, err := cryptoutil.DeriveKey(s.RecoveryAuthoritySalt + "|account-recovery|" + username)
	if err != nil {
		return nil, apperrors.Internal("failed to derive the recovery authority key")
	}
	message := fmt.Sprintf("wallet-backend account recovery\nusername: %s\nold address: %s\nnew address: %s", username, oldAddress, newAddress)
	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, authorityKey)
	if err != nil {
		return nil, apperrors.Internal("failed to sign the recovery attestation")
	}
	sig[64] += 27 // normalize to the v=27/28 form most tooling expects
	signatureHex := "0x" + common.Bytes2Hex(sig)

	return &models.AccountRecoveryLog{
		Username:           username,
		OldAddress:         oldAddress,
		NewAddress:         newAddress,
		AuthoritySignature: signatureHex,
	}, nil
}
