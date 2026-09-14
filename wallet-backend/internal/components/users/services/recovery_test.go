package services

import (
	"regexp"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/users/models"
)

// newRecoveryAddress generates a fresh address together with a valid
// personal_sign signature over RecoveryMessage(username, address), the
// proof-of-ownership Recover requires for whatever address recovery is
// pointing the account to.
func newRecoveryAddress(t *testing.T, username string) (address, signature string) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	address = crypto.PubkeyToAddress(key.PublicKey).Hex()
	hash := accounts.TextHash([]byte(RecoveryMessage(username, address)))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return address, "0x" + common.Bytes2Hex(sig)
}

// capturingMailer records every sent email so tests can pull the OTP out of
// the body without needing a real inbox.
type capturingMailer struct {
	lastTo, lastSubject, lastBody string
}

func (m *capturingMailer) Send(to, subject, body string) error {
	m.lastTo, m.lastSubject, m.lastBody = to, subject, body
	return nil
}

var otpPattern = regexp.MustCompile(`\d{6}`)

func (m *capturingMailer) extractOTP(t *testing.T) string {
	t.Helper()
	otp := otpPattern.FindString(m.lastBody)
	if otp == "" {
		t.Fatalf("no 6-digit OTP found in mail body: %q", m.lastBody)
	}
	return otp
}

// newRecoveryTestService builds a Service with a capturing mailer so tests
// can both drive the recovery flow and inspect what was emailed.
func newRecoveryTestService(t *testing.T, ttl time.Duration) (*Service, *capturingMailer) {
	t.Helper()
	mailer := &capturingMailer{}
	svc := New(newTestDB(t), mailer, "test-recovery-authority-salt", ttl, &fakeBlockchain{}, "test-deployer-salt")
	return svc, mailer
}

func registerWithRecoveryEnabled(t *testing.T, svc *Service, username string) (*models.User, []SecurityAnswerInput) {
	t.Helper()
	user, err := svc.Register(RegisterInput{Username: username, Email: username + "@example.com", SignerAddress: randomAddress(t)})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

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
			t.Fatalf("SetSecurityAnswer returned error: %v", err)
		}
	}

	if err := svc.EnableAccountRecovery(user.ID); err != nil {
		t.Fatalf("EnableAccountRecovery returned error: %v", err)
	}

	return user, answers
}

func TestRequestRecoveryOTP_SilentlyNoOpsForUnknownUsername(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	if err := svc.RequestRecoveryOTP("nobody"); err != nil {
		t.Fatalf("expected no error for an unknown username, got %v", err)
	}
	if mailer.lastBody != "" {
		t.Fatal("expected no email to be sent for an unknown username")
	}
}

func TestRequestRecoveryOTP_SilentlyNoOpsWhenRecoveryDisabled(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	if _, err := svc.Register(RegisterInput{Username: "dave", Email: "dave@example.com", SignerAddress: randomAddress(t)}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if err := svc.RequestRecoveryOTP("dave"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if mailer.lastBody != "" {
		t.Fatal("expected no email to be sent when recovery is not enabled")
	}
}

func TestRecover_Success_RepointsAddressAndRevokesGroupMembership(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	user, answers := registerWithRecoveryEnabled(t, svc, "alice")
	oldAddress := user.Address

	// Simulate the user still holding a shared-access group membership under
	// their old (lost) address - recovery should revoke it, not carry it
	// over to the new address.
	membership := sharedaccessModels.GroupMember{GroupID: 1, MemberAddress: oldAddress, Role: sharedaccessModels.RoleApprover}
	if err := svc.DB.AutoMigrate(&sharedaccessModels.GroupMember{}); err != nil {
		t.Fatalf("migrate sharedaccess models: %v", err)
	}
	if err := svc.DB.Create(&membership).Error; err != nil {
		t.Fatalf("create group membership: %v", err)
	}

	if err := svc.RequestRecoveryOTP("alice"); err != nil {
		t.Fatalf("RequestRecoveryOTP returned error: %v", err)
	}
	otp := mailer.extractOTP(t)

	newAddress, newAddressSig := newRecoveryAddress(t, "alice")
	logEntry, err := svc.Recover("alice", newAddress, newAddressSig, otp, answers)
	if err != nil {
		t.Fatalf("Recover returned error: %v", err)
	}
	if logEntry.OldAddress != oldAddress || logEntry.NewAddress != newAddress || logEntry.Username != "alice" {
		t.Fatalf("unexpected recovery log: %+v", logEntry)
	}
	if logEntry.AuthoritySignature == "" {
		t.Fatal("expected a non-empty authority signature")
	}

	updated, err := svc.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername returned error: %v", err)
	}
	if updated.Address != newAddress {
		t.Fatalf("expected address to be re-pointed to %s, got %s", newAddress, updated.Address)
	}

	var wallet models.UserWallet
	if err := svc.DB.Where("user_id = ? AND is_primary = ?", user.ID, true).First(&wallet).Error; err != nil {
		t.Fatalf("expected a primary wallet: %v", err)
	}
	if wallet.Address != newAddress {
		t.Fatalf("expected primary wallet address to be re-pointed to %s, got %s", newAddress, wallet.Address)
	}

	var remaining int64
	svc.DB.Model(&sharedaccessModels.GroupMember{}).Where("member_address = ?", oldAddress).Count(&remaining)
	if remaining != 0 {
		t.Fatalf("expected the old address's group membership to be revoked, found %d remaining", remaining)
	}

	// The OTP is single-use: replaying it must fail rather than succeed a
	// second time.
	anotherAddress, anotherSig := newRecoveryAddress(t, "alice")
	if _, err := svc.Recover("alice", anotherAddress, anotherSig, otp, answers); err == nil {
		t.Fatal("expected replaying a consumed OTP to fail")
	}
}

func TestRecover_RejectsWhenRecoveryNotEnabled(t *testing.T) {
	svc, _ := newRecoveryTestService(t, 15*time.Minute)
	if _, err := svc.Register(RegisterInput{Username: "eve", Email: "eve@example.com", SignerAddress: randomAddress(t)}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	newAddress, newAddressSig := newRecoveryAddress(t, "eve")
	_, err := svc.Recover("eve", newAddress, newAddressSig, "123456", nil)
	if err == nil {
		t.Fatal("expected an error when recovery is not enabled")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 403 {
		t.Fatalf("expected a 403 AppError, got %#v", err)
	}
}

func TestRecover_RejectsWrongSecurityAnswers(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	_, answers := registerWithRecoveryEnabled(t, svc, "frank")
	if err := svc.RequestRecoveryOTP("frank"); err != nil {
		t.Fatalf("RequestRecoveryOTP returned error: %v", err)
	}
	otp := mailer.extractOTP(t)

	wrongAnswers := make([]SecurityAnswerInput, len(answers))
	copy(wrongAnswers, answers)
	wrongAnswers[0].Answer = "definitely wrong"

	newAddress, newAddressSig := newRecoveryAddress(t, "frank")
	_, err := svc.Recover("frank", newAddress, newAddressSig, otp, wrongAnswers)
	if err == nil {
		t.Fatal("expected an error for an incorrect security answer")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 401 {
		t.Fatalf("expected a 401 AppError, got %#v", err)
	}
}

func TestRecover_RejectsIncompleteSecurityAnswers(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	_, answers := registerWithRecoveryEnabled(t, svc, "grace")
	if err := svc.RequestRecoveryOTP("grace"); err != nil {
		t.Fatalf("RequestRecoveryOTP returned error: %v", err)
	}
	otp := mailer.extractOTP(t)

	newAddress, newAddressSig := newRecoveryAddress(t, "grace")
	_, err := svc.Recover("grace", newAddress, newAddressSig, otp, answers[:1])
	if err == nil {
		t.Fatal("expected an error when not all configured security answers are supplied")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 400 {
		t.Fatalf("expected a 400 AppError, got %#v", err)
	}
}

func TestRecover_RejectsWrongOTP(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	_, answers := registerWithRecoveryEnabled(t, svc, "heidi")
	if err := svc.RequestRecoveryOTP("heidi"); err != nil {
		t.Fatalf("RequestRecoveryOTP returned error: %v", err)
	}
	mailer.extractOTP(t) // drain, but deliberately use a wrong code below

	newAddress, newAddressSig := newRecoveryAddress(t, "heidi")
	_, err := svc.Recover("heidi", newAddress, newAddressSig, "000000", answers)
	if err == nil {
		t.Fatal("expected an error for an incorrect OTP")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 401 {
		t.Fatalf("expected a 401 AppError, got %#v", err)
	}
}

func TestRecover_RejectsExpiredOTP(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 1*time.Nanosecond)
	_, answers := registerWithRecoveryEnabled(t, svc, "ivan")
	if err := svc.RequestRecoveryOTP("ivan"); err != nil {
		t.Fatalf("RequestRecoveryOTP returned error: %v", err)
	}
	otp := mailer.extractOTP(t)
	time.Sleep(time.Millisecond)

	newAddress, newAddressSig := newRecoveryAddress(t, "ivan")
	_, err := svc.Recover("ivan", newAddress, newAddressSig, otp, answers)
	if err == nil {
		t.Fatal("expected an error for an expired OTP")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 401 {
		t.Fatalf("expected a 401 AppError, got %#v", err)
	}
}

func TestRecover_RejectsInvalidNewAddress(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	_, answers := registerWithRecoveryEnabled(t, svc, "judy")
	if err := svc.RequestRecoveryOTP("judy"); err != nil {
		t.Fatalf("RequestRecoveryOTP returned error: %v", err)
	}
	otp := mailer.extractOTP(t)

	_, err := svc.Recover("judy", "not-an-address", "0xdead", otp, answers)
	if err == nil {
		t.Fatal("expected an error for an invalid new address")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 400 {
		t.Fatalf("expected a 400 AppError, got %#v", err)
	}
}

func TestRecover_RejectsForgedNewAddressSignature(t *testing.T) {
	svc, mailer := newRecoveryTestService(t, 15*time.Minute)
	_, answers := registerWithRecoveryEnabled(t, svc, "leo")
	if err := svc.RequestRecoveryOTP("leo"); err != nil {
		t.Fatalf("RequestRecoveryOTP returned error: %v", err)
	}
	otp := mailer.extractOTP(t)

	// Sign for one address but claim a different one as newAddress - the
	// attacker doesn't control the claimed address's key.
	_, signatureForADifferentAddress := newRecoveryAddress(t, "leo")
	unrelatedAddress := randomAddress(t)

	_, err := svc.Recover("leo", unrelatedAddress, signatureForADifferentAddress, otp, answers)
	if err == nil {
		t.Fatal("expected an error for a signature that doesn't match newAddress")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 401 {
		t.Fatalf("expected a 401 AppError, got %#v", err)
	}
}

func TestEnableDisableAccountRecovery(t *testing.T) {
	svc, _ := newRecoveryTestService(t, 15*time.Minute)
	user, err := svc.Register(RegisterInput{Username: "kevin", Email: "kevin@example.com", SignerAddress: randomAddress(t)})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	fetched, err := svc.GetByUsername("kevin")
	if err != nil || fetched.AccountRecoveryEnabled {
		t.Fatalf("expected recovery to start disabled: %v %+v", err, fetched)
	}

	if err := svc.EnableAccountRecovery(user.ID); err != nil {
		t.Fatalf("EnableAccountRecovery returned error: %v", err)
	}
	fetched, err = svc.GetByUsername("kevin")
	if err != nil || !fetched.AccountRecoveryEnabled {
		t.Fatalf("expected recovery to be enabled: %v %+v", err, fetched)
	}

	if err := svc.DisableAccountRecovery(user.ID); err != nil {
		t.Fatalf("DisableAccountRecovery returned error: %v", err)
	}
	fetched, err = svc.GetByUsername("kevin")
	if err != nil || fetched.AccountRecoveryEnabled {
		t.Fatalf("expected recovery to be disabled again: %v %+v", err, fetched)
	}
}

func TestRegister_RejectsReservedUsername(t *testing.T) {
	svc, _ := newRecoveryTestService(t, 15*time.Minute)
	if err := svc.DB.Create(&models.ReservedName{Name: "admin"}).Error; err != nil {
		t.Fatalf("create reserved name: %v", err)
	}

	_, err := svc.Register(RegisterInput{Username: "admin", Email: "admin@example.com", SignerAddress: randomAddress(t)})
	if err == nil {
		t.Fatal("expected an error for a reserved username")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 409 {
		t.Fatalf("expected a 409 AppError, got %#v", err)
	}
}
