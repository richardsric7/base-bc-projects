package models

// The types below are Stablerail API request/response shapes, not
// persisted models - ported near-verbatim from the original since they
// mirror Stablerail's actual REST contract.

type OnboardRequest struct {
	BVN string `json:"bvn"`
}

type OnboardResponse struct {
	ResponseCode string `json:"response_code"`
	Message      string `json:"message"`
	Data         struct {
		RequestID string `json:"requestId"`
		Status    string `json:"status"`
		UserHash  string `json:"userHash"`
		Message   string `json:"message"`
	} `json:"data"`
}

type OnboardingStatusResponse struct {
	ResponseCode string `json:"response_code"`
	Data         struct {
		UserID string `json:"userId"`
		Status string `json:"status"`
	} `json:"data"`
}

// OnrampRequest asks Stablerail to create an NGN->stablecoin conversion,
// sweeping the result straight to the off-ramp step (this port always sets
// SweepToOfframp - the Base withdrawal is that off-ramp, triggered
// automatically once funded; see services.go) rather than leaving funds
// parked in a Stablerail-managed wallet as AutoSwap=false would.
type OnrampRequest struct {
	Amount         float64 `json:"amount"`
	AssetSwap      string  `json:"assetSwap"`
	AutoSwap       bool    `json:"autoSwap"`
	UserID         string  `json:"userId"`
	SweepToOfframp bool    `json:"sweepToOfframp"`
}

type OnrampResponse struct {
	ResponseCode string `json:"response_code"`
	Message      string `json:"message"`
	Data         struct {
		RequestID     string `json:"requestId"`
		WalletAddress string `json:"walletAddress"`
		Status        string `json:"status"`
	} `json:"data"`
}

type VirtualAccountRequest struct {
	RequestID string `json:"requestId"`
}

type VirtualAccountResponse struct {
	ResponseCode string `json:"response_code"`
	Data         struct {
		Status         string `json:"status"`
		WalletAddress  string `json:"walletAddress"`
		VirtualAccount struct {
			AccountNumber string  `json:"accountNumber"`
			BankName      string  `json:"bankName"`
			AccountName   string  `json:"accountName"`
			Amount        float64 `json:"amount"`
		} `json:"virtualAccount"`
	} `json:"data"`
}

type OnrampStatusRequest struct {
	WalletAddress string `json:"walletAddress"`
}

type OnrampStatusResponse struct {
	ResponseCode string `json:"response_code"`
	Data         struct {
		Status    string `json:"status"`
		RequestID string `json:"requestId"`
		Wallet    struct {
			WalletAddress string `json:"walletAddress"`
			TokenBuy      string `json:"tokenBuy"`
			AutoSwap      bool   `json:"autoSwap"`
			Amount        string `json:"amount"`
		} `json:"wallet"`
	} `json:"data"`
}

// AssetWithdrawalRequest asks Stablerail to move previously on-ramped
// funds from its internal wallet to the user's own Base address - the
// actual on-chain transfer happens on Stablerail's side, not this
// backend's, so no network.Client is involved in this component at all.
type AssetWithdrawalRequest struct {
	ID                string  `json:"id"` // the onramp request ID this withdrawal settles
	UserID            string  `json:"userId"`
	InternalWallet    string  `json:"internalWallet"`
	DestinationWallet string  `json:"destinationWallet"`
	Amount            float64 `json:"amount"`
	Ticker            string  `json:"ticker"`
	Network           string  `json:"network"`
}

type AssetWithdrawalResponse struct {
	ResponseCode string `json:"response_code"`
	Message      string `json:"message"`
}

type BanksResponse struct {
	Data struct {
		CountryCode string `json:"countryCode"`
		Banks       []struct {
			BankCode string `json:"bank_code"`
			BankName string `json:"bank_name"`
		} `json:"banks"`
	} `json:"data"`
}
