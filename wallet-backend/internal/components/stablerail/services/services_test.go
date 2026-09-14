package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/stablerail/models"
	usersModels "wallet-backend/internal/components/users/models"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate stablerail models: %v", err)
	}
	if err := db.AutoMigrate(usersModels.Models...); err != nil {
		t.Fatalf("migrate users models: %v", err)
	}
	return db
}

func createTestUser(t *testing.T, db *gorm.DB, username string) usersModels.User {
	t.Helper()
	address := "0x" + username + "0000000000000000000000000000000000"
	user := usersModels.User{
		Username:      username,
		Email:         username + "@example.com",
		Address:       address,
		SignerAddress: address,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

func newTestService(t *testing.T, serverURL string, enabled bool) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	svc := New(db, "test-api-key", serverURL, enabled)
	return svc, db
}

func appErrStatus(t *testing.T, err error) int {
	t.Helper()
	appErr, ok := err.(*apperrors.AppError)
	if !ok {
		t.Fatalf("expected an *apperrors.AppError, got %#v", err)
	}
	return appErr.StatusCode()
}

// fakeStablerailServer builds an httptest server that plays along with
// whichever endpoints a test needs, keyed by path -> JSON response.
func fakeStablerailServer(t *testing.T, responses map[string]interface{}) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, response := range responses {
		resp := response
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(resp)
		})
	}
	return httptest.NewServer(mux)
}

func TestInitiateOnboarding_Disabled(t *testing.T) {
	svc, db := newTestService(t, "", false)
	user := createTestUser(t, db, "alice")

	_, err := svc.InitiateOnboarding(user.Address, "12345678901")
	if err == nil {
		t.Fatal("expected an error when Stablerail is disabled")
	}
	if status := appErrStatus(t, err); status != 202 {
		t.Fatalf("expected a 202 AppError, got %d", status)
	}
}

func TestInitiateOnboarding_Success(t *testing.T) {
	server := fakeStablerailServer(t, map[string]interface{}{
		"/onboarduser": models.OnboardResponse{
			ResponseCode: "00",
			Data: struct {
				RequestID string `json:"requestId"`
				Status    string `json:"status"`
				UserHash  string `json:"userHash"`
				Message   string `json:"message"`
			}{RequestID: "req-1", Status: "processing", Message: "onboarding started"},
		},
	})
	defer server.Close()
	svc, db := newTestService(t, server.URL, true)
	user := createTestUser(t, db, "bob")

	msg, err := svc.InitiateOnboarding(user.Address, "12345678901")
	if err != nil {
		t.Fatalf("InitiateOnboarding returned error: %v", err)
	}
	if msg != "onboarding started" {
		t.Fatalf("unexpected message: %q", msg)
	}

	var request models.StablerailRequest
	if err := db.Where("id = ?", "req-1").First(&request).Error; err != nil {
		t.Fatalf("expected a saved StablerailRequest: %v", err)
	}
	if request.RequestType != "Onboarding" || request.Status != "processing" || request.UserID != user.ID {
		t.Fatalf("unexpected request row: %+v", request)
	}
}

func TestInitiateOnboarding_AlreadyRegisteredSkipsAPICall(t *testing.T) {
	svc, db := newTestService(t, "http://should-not-be-called.invalid", true)
	user := createTestUser(t, db, "carol")
	if err := db.Create(&models.StablerailUser{ID: "sr-user-1", UserID: user.ID}).Error; err != nil {
		t.Fatalf("seed stablerail user: %v", err)
	}

	msg, err := svc.InitiateOnboarding(user.Address, "12345678901")
	if err != nil {
		t.Fatalf("InitiateOnboarding returned error: %v", err)
	}
	if msg != "registration already done" {
		t.Fatalf("unexpected message: %q", msg)
	}
}

func TestPollPendingOnboarding_MarksCompleted(t *testing.T) {
	server := fakeStablerailServer(t, map[string]interface{}{
		"/onboardstatus": models.OnboardingStatusResponse{
			ResponseCode: "00",
			Data: struct {
				UserID string `json:"userId"`
				Status string `json:"status"`
			}{UserID: "sr-user-2", Status: "completed"},
		},
	})
	defer server.Close()
	svc, db := newTestService(t, server.URL, true)
	user := createTestUser(t, db, "dave")
	if err := db.Create(&models.StablerailRequest{ID: "req-2", UserID: user.ID, RequestType: "Onboarding", Status: "processing"}).Error; err != nil {
		t.Fatalf("seed request: %v", err)
	}

	svc.PollPendingOnboarding()

	var stablerailUser models.StablerailUser
	if err := db.Where("user_id = ?", user.ID).First(&stablerailUser).Error; err != nil {
		t.Fatalf("expected a StablerailUser to be created: %v", err)
	}
	if stablerailUser.ID != "sr-user-2" {
		t.Fatalf("unexpected stablerail user ID: %q", stablerailUser.ID)
	}

	var request models.StablerailRequest
	db.Where("id = ?", "req-2").First(&request)
	if request.Status != "completed" {
		t.Fatalf("expected request status updated to completed, got %q", request.Status)
	}
}

func TestInitiateOnramp_RequiresOnboarding(t *testing.T) {
	svc, db := newTestService(t, "", true)
	user := createTestUser(t, db, "erin")

	_, err := svc.InitiateOnramp(user.Address, 5000)
	if err == nil {
		t.Fatal("expected an error for a user who hasn't completed onboarding")
	}
	if status := appErrStatus(t, err); status != 404 {
		t.Fatalf("expected a 404 AppError, got %d", status)
	}
}

func TestInitiateOnramp_Success(t *testing.T) {
	server := fakeStablerailServer(t, map[string]interface{}{
		"/cngnonramp": models.OnrampResponse{
			ResponseCode: "00",
			Data: struct {
				RequestID     string `json:"requestId"`
				WalletAddress string `json:"walletAddress"`
				Status        string `json:"status"`
			}{RequestID: "req-3", WalletAddress: "sr-wallet-1", Status: "created"},
		},
		"/getvirtualaccount": models.VirtualAccountResponse{
			ResponseCode: "00",
			Data: struct {
				Status         string `json:"status"`
				WalletAddress  string `json:"walletAddress"`
				VirtualAccount struct {
					AccountNumber string  `json:"accountNumber"`
					BankName      string  `json:"bankName"`
					AccountName   string  `json:"accountName"`
					Amount        float64 `json:"amount"`
				} `json:"virtualAccount"`
			}{
				Status: "created",
				VirtualAccount: struct {
					AccountNumber string  `json:"accountNumber"`
					BankName      string  `json:"bankName"`
					AccountName   string  `json:"accountName"`
					Amount        float64 `json:"amount"`
				}{AccountNumber: "0123456789", BankName: "Test Bank", AccountName: "Trovo/erin", Amount: 5000},
			},
		},
	})
	defer server.Close()
	svc, db := newTestService(t, server.URL, true)
	user := createTestUser(t, db, "frank")
	if err := db.Create(&models.StablerailUser{ID: "sr-user-3", UserID: user.ID}).Error; err != nil {
		t.Fatalf("seed stablerail user: %v", err)
	}

	result, err := svc.InitiateOnramp(user.Address, 5000)
	if err != nil {
		t.Fatalf("InitiateOnramp returned error: %v", err)
	}
	if result.AccountNumber != "0123456789" || result.Amount != 5000 {
		t.Fatalf("unexpected onramp result: %+v", result)
	}

	var onramp models.StablerailOnramp
	if err := db.Where("id = ?", "req-3").First(&onramp).Error; err != nil {
		t.Fatalf("expected a saved onramp row: %v", err)
	}
	if onramp.DestinationAddress != user.Address {
		t.Fatalf("expected destination address to be the user's Base address, got %q", onramp.DestinationAddress)
	}
}

func TestPollPendingOnramp_FundedTriggersWithdrawal(t *testing.T) {
	server := fakeStablerailServer(t, map[string]interface{}{
		"/cngnonrampstatus": models.OnrampStatusResponse{
			ResponseCode: "00",
			Data: struct {
				Status    string `json:"status"`
				RequestID string `json:"requestId"`
				Wallet    struct {
					WalletAddress string `json:"walletAddress"`
					TokenBuy      string `json:"tokenBuy"`
					AutoSwap      bool   `json:"autoSwap"`
					Amount        string `json:"amount"`
				} `json:"wallet"`
			}{
				Status:    "funded",
				RequestID: "req-4",
				Wallet: struct {
					WalletAddress string `json:"walletAddress"`
					TokenBuy      string `json:"tokenBuy"`
					AutoSwap      bool   `json:"autoSwap"`
					Amount        string `json:"amount"`
				}{WalletAddress: "sr-wallet-2", TokenBuy: "USDC", Amount: "5000"},
			},
		},
		"/withdrawasset": models.AssetWithdrawalResponse{ResponseCode: "00", Message: "Withdrawal initiated"},
	})
	defer server.Close()
	svc, db := newTestService(t, server.URL, true)
	user := createTestUser(t, db, "grace")
	if err := db.Create(&models.StablerailUser{ID: "sr-user-4", UserID: user.ID}).Error; err != nil {
		t.Fatalf("seed stablerail user: %v", err)
	}
	if err := db.Create(&models.StablerailOnramp{
		ID: "req-4", UserID: user.ID, WalletAddress: "sr-wallet-2", DestinationAddress: user.Address,
		TotalAmount: 5000, TargetAsset: "USDC", Status: "created",
	}).Error; err != nil {
		t.Fatalf("seed onramp: %v", err)
	}
	if err := db.Create(&models.StablerailRequest{ID: "req-4", UserID: user.ID, RequestType: "Onramp", Status: "created"}).Error; err != nil {
		t.Fatalf("seed request: %v", err)
	}

	svc.PollPendingOnramp()

	var withdrawal models.StablerailAssetWithdrawal
	if err := db.Where("id = ?", "req-4").First(&withdrawal).Error; err != nil {
		t.Fatalf("expected a withdrawal row: %v", err)
	}
	if withdrawal.Status != "funded" || withdrawal.DestinationWallet != user.Address || withdrawal.Network != "base" {
		t.Fatalf("unexpected withdrawal row: %+v", withdrawal)
	}
}

func TestListBanksAndSync(t *testing.T) {
	server := fakeStablerailServer(t, map[string]interface{}{
		"/getbankscode": models.BanksResponse{
			Data: struct {
				CountryCode string `json:"countryCode"`
				Banks       []struct {
					BankCode string `json:"bank_code"`
					BankName string `json:"bank_name"`
				} `json:"banks"`
			}{
				CountryCode: "NG",
				Banks: []struct {
					BankCode string `json:"bank_code"`
					BankName string `json:"bank_name"`
				}{{BankCode: "001", BankName: "Test Bank"}},
			},
		},
	})
	defer server.Close()
	svc, _ := newTestService(t, server.URL, true)

	if err := svc.SyncSupportedBanks(); err != nil {
		t.Fatalf("SyncSupportedBanks returned error: %v", err)
	}

	banks, err := svc.ListBanks()
	if err != nil {
		t.Fatalf("ListBanks returned error: %v", err)
	}
	if len(banks) != 1 || banks[0].BankCode != "001" || banks[0].CountryCode != "NG" {
		t.Fatalf("unexpected banks: %+v", banks)
	}
}
