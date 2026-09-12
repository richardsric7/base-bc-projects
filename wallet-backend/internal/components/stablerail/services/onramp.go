package services

import (
	"strings"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/stablerail/models"
)

// OnrampResult is what a client needs to complete their bank transfer.
type OnrampResult struct {
	RequestID     string  `json:"requestId"`
	AccountNumber string  `json:"accountNumber"`
	BankName      string  `json:"bankName"`
	AccountName   string  `json:"accountName"`
	Amount        float64 `json:"amount"`
}

// InitiateOnramp starts an NGN->stablecoin on-ramp for amountNGN, returning
// the virtual account the client must pay into. Ported from the original's
// StablerailInitiateCNGNOnrampRequest, minus its pending-request reuse
// branch (an operational nicety, not core business logic - a caller who
// already has one pending gets a second, which Stablerail's own API is
// free to reject or allow) and its SMS/push notifications (no device-token
// subsystem exists yet - see PLAN.md §4.13).
func (s *Service) InitiateOnramp(address string, amountNGN float64) (*OnrampResult, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}

	var stablerailUser models.StablerailUser
	if err := s.DB.Where("user_id = ?", user.ID).First(&stablerailUser).Error; err != nil {
		return nil, apperrors.New(404, "not_onboarded", "complete BVN onboarding before requesting a deposit")
	}

	var onrampResp models.OnrampResponse
	req := models.OnrampRequest{
		Amount:         amountNGN,
		AssetSwap:      "USDC",
		AutoSwap:       true,
		UserID:         stablerailUser.ID,
		SweepToOfframp: true,
	}
	if err := s.doRequest("POST", "/cngnonramp", req, &onrampResp); err != nil {
		return nil, err
	}
	if onrampResp.ResponseCode != "00" {
		return nil, apperrors.Internal("Stablerail declined the deposit request: " + onrampResp.Message)
	}

	onramp := models.StablerailOnramp{
		ID:                 onrampResp.Data.RequestID,
		UserID:             user.ID,
		WalletAddress:      onrampResp.Data.WalletAddress,
		DestinationAddress: user.Address,
		TotalAmount:        amountNGN,
		TargetAsset:        "USDC",
		Status:             onrampResp.Data.Status,
		AutoSwapEnabled:    true,
	}
	if err := s.DB.Create(&onramp).Error; err != nil {
		return nil, apperrors.Internal("failed to save deposit request")
	}
	if err := s.DB.Create(&models.StablerailRequest{
		ID:          onrampResp.Data.RequestID,
		UserID:      user.ID,
		RequestType: "Onramp",
		Status:      onrampResp.Data.Status,
	}).Error; err != nil {
		return nil, apperrors.Internal("failed to save deposit request")
	}

	var vaResp models.VirtualAccountResponse
	if err := s.doRequest("POST", "/getvirtualaccount", models.VirtualAccountRequest{RequestID: onrampResp.Data.RequestID}, &vaResp); err != nil {
		return nil, err
	}

	return &OnrampResult{
		RequestID:     onrampResp.Data.RequestID,
		AccountNumber: vaResp.Data.VirtualAccount.AccountNumber,
		BankName:      vaResp.Data.VirtualAccount.BankName,
		AccountName:   vaResp.Data.VirtualAccount.AccountName,
		Amount:        vaResp.Data.VirtualAccount.Amount,
	}, nil
}

// PollPendingOnramp checks every in-flight on-ramp against Stablerail's
// status endpoint and, once funded, triggers the withdrawal of the
// converted stablecoin to the user's Base address - meant to be called on
// an interval by a background goroutine (see main.go), mirroring the
// original's ProcessUpdateStablerailCNGNOnrampStatus.
func (s *Service) PollPendingOnramp() {
	if err := s.requireEnabled(); err != nil {
		return
	}
	var pending []models.StablerailRequest
	if err := s.DB.Where("request_type = ? AND status LIKE ?", "Onramp", "%created%").Find(&pending).Error; err != nil {
		return
	}
	for _, request := range pending {
		if err := s.pollOnrampRequest(request); err != nil {
			logPollError("onramp", request.ID, err)
		}
	}
}

func (s *Service) pollOnrampRequest(request models.StablerailRequest) error {
	var onramp models.StablerailOnramp
	if err := s.DB.Where("id = ?", request.ID).First(&onramp).Error; err != nil {
		return err
	}

	var resp models.OnrampStatusResponse
	if err := s.doRequest("POST", "/cngnonrampstatus", models.OnrampStatusRequest{WalletAddress: onramp.WalletAddress}, &resp); err != nil {
		return err
	}
	if resp.ResponseCode != "00" {
		return apperrors.Internal("could not check deposit status")
	}

	request.Status = resp.Data.Status
	if err := s.DB.Save(&request).Error; err != nil {
		return err
	}

	if !strings.EqualFold(resp.Data.Status, "funded") {
		return nil
	}

	onramp.Status = resp.Data.Status
	onramp.TargetAsset = resp.Data.Wallet.TokenBuy
	if err := s.DB.Save(&onramp).Error; err != nil {
		return err
	}

	return s.initiateAssetWithdrawal(onramp)
}

// initiateAssetWithdrawal asks Stablerail to move a funded on-ramp's
// converted balance from its internal wallet to the user's Base address -
// the on-chain transfer itself happens on Stablerail's side. Ported from
// the original's StableRailInitiateAssetWithdrawal, dropping its
// internal-balance-asset guard (that concept - an off-chain ledger balance
// that can't be withdrawn - was never ported to this project; every
// balance here is a real on-chain balance).
func (s *Service) initiateAssetWithdrawal(onramp models.StablerailOnramp) error {
	var stablerailUser models.StablerailUser
	if err := s.DB.Where("user_id = ?", onramp.UserID).First(&stablerailUser).Error; err != nil {
		return err
	}

	withdrawal := models.StablerailAssetWithdrawal{
		ID:                onramp.ID,
		UserID:            onramp.UserID,
		InternalWallet:    onramp.WalletAddress,
		DestinationWallet: onramp.DestinationAddress,
		Amount:            onramp.TotalAmount,
		Ticker:            onramp.TargetAsset,
		Network:           network,
		Status:            "pending",
	}
	if err := s.DB.Create(&withdrawal).Error; err != nil {
		return err
	}

	var resp models.AssetWithdrawalResponse
	req := models.AssetWithdrawalRequest{
		ID:                onramp.ID,
		UserID:            stablerailUser.ID,
		InternalWallet:    onramp.WalletAddress,
		DestinationWallet: onramp.DestinationAddress,
		Amount:            onramp.TotalAmount,
		Ticker:            onramp.TargetAsset,
		Network:           network,
	}
	if err := s.doRequest("POST", "/withdrawasset", req, &resp); err != nil {
		return err
	}
	if resp.ResponseCode == "00" {
		withdrawal.Status = "funded"
		return s.DB.Save(&withdrawal).Error
	}
	return apperrors.Internal("Stablerail declined the withdrawal: " + resp.Message)
}

// ListBanks returns Stablerail's synced supported-bank list.
func (s *Service) ListBanks() ([]models.StablerailBank, error) {
	var banks []models.StablerailBank
	if err := s.DB.Order("bank_name ASC").Find(&banks).Error; err != nil {
		return nil, apperrors.Internal("failed to load banks")
	}
	return banks, nil
}

// SyncSupportedBanks fetches Stablerail's current bank list and upserts it
// - meant to be called on a long interval by a background goroutine (see
// main.go), mirroring the original's StablerailSaveSupportedBanks.
func (s *Service) SyncSupportedBanks() error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	var resp models.BanksResponse
	if err := s.doRequest("GET", "/getbankscode", nil, &resp); err != nil {
		return err
	}
	for _, bank := range resp.Data.Banks {
		s.DB.Save(&models.StablerailBank{
			BankCode:    bank.BankCode,
			BankName:    bank.BankName,
			CountryCode: resp.Data.CountryCode,
		})
	}
	return nil
}
