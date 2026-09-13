package models

// The types below are OneLiquidity API request/response shapes, not
// persisted models - ported near-verbatim from the original since they
// mirror OneLiquidity's actual REST contract.

type SubwalletRequest struct {
	Currency string `json:"currency"`
	UID      string `json:"uid"`
}

type CryptoAddress struct {
	Address string `json:"address"`
	Network string `json:"network"`
}

type CryptoSubWallet struct {
	WalletID  string          `json:"walletId"`
	UID       string          `json:"uid"`
	Currency  string          `json:"currency"`
	Addresses []CryptoAddress `json:"addresses"`
}

type SubwalletResponse struct {
	Message string          `json:"message"`
	Data    CryptoSubWallet `json:"data"`
}

type DepositItem struct {
	DepositID   string `json:"depositId"`
	TxID        string `json:"txId"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Fees        string `json:"fees"`
	FromAddress string `json:"fromAddress"`
	ToAddress   string `json:"toAddress"`
	IsCompleted bool   `json:"isCompleted"`
	IsValid     bool   `json:"isValid"`
	IsVerified  bool   `json:"isVerified"`
}

type DepositResponse struct {
	Message string      `json:"message"`
	Data    DepositItem `json:"data"`
}

type DepositListResponse struct {
	Message string        `json:"message"`
	Data    []DepositItem `json:"data"`
}

type WithdrawalNetworkItem struct {
	Network     string `json:"network"`
	WithdrawMin string `json:"withdrawMin"`
	WithdrawMax string `json:"withdrawMax"`
	WithdrawFee string `json:"withdrawFee"`
}

type WithdrawalNetworksResponse struct {
	Message string                  `json:"message"`
	Data    []WithdrawalNetworkItem `json:"data"`
}

type WithdrawalRequestPayload struct {
	Currency  string  `json:"currency"`
	Amount    float64 `json:"amount"`
	ToAddress string  `json:"toAddress"`
	Network   string  `json:"network"`
	Reference string  `json:"reference"` // this port's idempotency reference (our WithdrawalRequest.ID)
}

type WithdrawalResponse struct {
	Message string `json:"message"`
	Data    struct {
		WithdrawalID string `json:"withdrawalId"`
		Status       string `json:"status"`
	} `json:"data"`
}
