// Package services implements the fiat component's business logic: the
// decoupled invoice pattern internal/fiat's doc comment describes, and the
// "activation" flow that dispenses starter gas and a reward token once a
// user pays a configured fiat amount. See PLAN.md §4.5 for the full design
// and the deviations from the original documented there.
package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/fiat/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
	"wallet-backend/internal/rates"
)

// BlockchainClient is the narrow slice of *network.Client this component
// needs - narrowed to an interface (same pattern as sharedaccess and kyc's
// dependents) so the activation-dispense and asset-purchase-submission
// paths are unit-testable without a live Base RPC.
type BlockchainClient interface {
	SignAndSubmitTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
	SubmitSignedTransaction(ctx context.Context, rawTxHex string) (string, error)
}

type Service struct {
	DB                *gorm.DB
	Blockchain        BlockchainClient
	Rates             rates.Provider
	FaucetKeySalt     string
	RewardTokenSymbol string // empty disables the reward-token half of activation
}

func New(db *gorm.DB, blockchain BlockchainClient, ratesProvider rates.Provider, faucetKeySalt, rewardTokenSymbol string) *Service {
	return &Service{
		DB:                db,
		Blockchain:        blockchain,
		Rates:             ratesProvider,
		FaucetKeySalt:     faucetKeySalt,
		RewardTokenSymbol: rewardTokenSymbol,
	}
}

// deriveFaucetKey derives the single server-controlled key that funds
// activation payouts, the same try-and-increment derivation used for
// shared-access group keys and the recovery-authority key
// (cryptoutil.DeriveKey). The original stored a live Stellar secret key
// for this role directly in a database row (FaucetConfig); deriving it
// instead means there is no faucet private key at rest anywhere - an
// operator funds this one address with ETH and the reward token ahead of
// time, the same operational step as before, just without a plaintext key
// in the database.
func (s *Service) deriveFaucetKey() (*ecdsa.PrivateKey, error) {
	return cryptoutil.DeriveKey(s.FaucetKeySalt + "|activation-faucet")
}

func (s *Service) getUserByAddress(address string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("address = ?", address).First(&user).Error; err != nil {
		return nil, apperrors.NotFound("user profile not found - register first")
	}
	return &user, nil
}

func (s *Service) getUserByUsername(username string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, apperrors.NotFound("unknown username in webhook payload: " + username)
	}
	return &user, nil
}

// LogWebhook records a permanent audit row for every Flutterwave webhook
// received, mirroring the audit-log pattern used for Sumsub - called by
// the controller right after signature verification passes, before any
// business logic runs.
func (s *Service) LogWebhook(event, txRef string) {
	s.DB.Create(&models.FlutterwaveWebhookLog{Event: event, TxRef: txRef})
}

func (s *Service) activationConfig() (*models.ActivationConfig, error) {
	var config models.ActivationConfig
	if err := s.DB.Order("id ASC").First(&config).Error; err != nil {
		return nil, apperrors.Internal("no activation config found - has the service been seeded?")
	}
	return &config, nil
}

// ActivationQuote is what a client needs to know before paying to activate.
type ActivationQuote struct {
	AlreadyActivated   bool    `json:"alreadyActivated"`
	FiatAmount         float64 `json:"fiatAmount"`
	FiatCurrency       string  `json:"fiatCurrency"`
	RewardTokenPercent float64 `json:"rewardTokenPercent"`
	GasPercent         float64 `json:"gasPercent"`
}

// GetActivationQuote returns the current activation price, or
// AlreadyActivated=true with a zero amount if the caller already completed
// activation - mirroring the original's GetFiatActiationAmount, minus its
// per-country lookup (see ActivationConfig's doc comment).
func (s *Service) GetActivationQuote(address string) (*ActivationQuote, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	config, err := s.activationConfig()
	if err != nil {
		return nil, err
	}
	if user.Activated {
		return &ActivationQuote{AlreadyActivated: true, FiatCurrency: config.FiatCurrency}, nil
	}
	return &ActivationQuote{
		FiatAmount:         config.FiatActivationAmount,
		FiatCurrency:       config.FiatCurrency,
		RewardTokenPercent: config.RewardTokenPercent,
		GasPercent:         100 - config.RewardTokenPercent,
	}, nil
}

// CreateInvoice records a PENDING invoice for id (the client's own
// idempotency reference, reused as the payment provider's transaction
// reference) before the client pays - see internal/fiat's doc comment for
// why this is a separate step from the webhook that completes it.
// signedTransaction is optional: the activation flow doesn't need one (the
// faucet, not the client, sends a transaction), but a future
// payment-type - like an asset purchase - can attach a pre-signed
// transaction here for ProcessFlutterwebhook to submit once payment clears.
func (s *Service) CreateInvoice(address, id, serviceProvider, paymentType string, amount float64, currency string, signedTransaction *string) (*models.FiatPaymentInvoice, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	invoice := models.FiatPaymentInvoice{
		ID:                id,
		UserID:            user.ID,
		ServiceProvider:   serviceProvider,
		PaymentType:       paymentType,
		Amount:            amount,
		Currency:          currency,
		Status:            "PENDING",
		SignedTransaction: signedTransaction,
	}
	if err := s.DB.Create(&invoice).Error; err != nil {
		return nil, apperrors.Conflict("an invoice with this reference already exists")
	}
	return &invoice, nil
}

// ListInvoices and ListPayments back the "my fiat activity" screen -
// mirrors the original's getUsersFiatPaymentsHandler.
func (s *Service) ListInvoices(address string) ([]models.FiatPaymentInvoice, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	var invoices []models.FiatPaymentInvoice
	if err := s.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Find(&invoices).Error; err != nil {
		return nil, apperrors.Internal("failed to load invoices")
	}
	return invoices, nil
}

func (s *Service) ListPayments(address string) ([]models.FiatPayment, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	var payments []models.FiatPayment
	if err := s.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Find(&payments).Error; err != nil {
		return nil, apperrors.Internal("failed to load payments")
	}
	return payments, nil
}

// toBaseUnits converts a decimal quantity into an integer amount in the
// given number of on-chain decimals (18 for ETH, whatever a CuratedToken
// declares for an ERC-20), truncating any precision beyond that - the same
// rounding direction the original's Truncate(7) used for Stellar amounts.
func toBaseUnits(amount decimal.Decimal, decimals uint8) *big.Int {
	scaled := amount.Shift(int32(decimals)).Truncate(0)
	result, _ := new(big.Int).SetString(scaled.String(), 10)
	if result == nil {
		return big.NewInt(0)
	}
	return result
}

// ProcessActivation dispenses starter gas and, if configured, a reward
// token from the derived faucet key, then marks user activated. Guarded
// against double-dispensing: if the user is already Activated this is a
// no-op success (so a duplicate webhook delivery for the same charge never
// pays out twice). Ported from the original's activation branch of
// postCallbacksFlutterwaveWebhookHandler; see PLAN.md §4.5 for what's
// simplified (a single global split rather than per-country) and what's
// deferred (no push notification - no device-token subsystem exists yet).
func (s *Service) ProcessActivation(ctx context.Context, username, providerReference string, fiatAmount decimal.Decimal, fiatCurrency string) error {
	user, err := s.getUserByUsername(username)
	if err != nil {
		return err
	}
	if user.Activated {
		return nil
	}

	config, err := s.activationConfig()
	if err != nil {
		return err
	}

	rewardShare := decimal.NewFromFloat(config.RewardTokenPercent).Div(decimal.NewFromInt(100))
	rewardFiatAmount := fiatAmount.Mul(rewardShare)
	gasFiatAmount := fiatAmount.Sub(rewardFiatAmount)

	faucetKey, err := s.deriveFaucetKey()
	if err != nil {
		return apperrors.Internal("failed to derive the activation faucet key")
	}
	toAddr := common.HexToAddress(user.Address)

	gasRate, err := s.Rates.GetRate(ctx, fiatCurrency, "ETH")
	if err != nil {
		return apperrors.Internal("no exchange rate configured for " + fiatCurrency + "/ETH")
	}
	gasWei := toBaseUnits(gasFiatAmount.Mul(gasRate), 18)
	if _, err := s.Blockchain.SignAndSubmitTx(ctx, faucetKey, &toAddr, gasWei, nil, nil); err != nil {
		return apperrors.Internal("failed to dispense activation gas: " + err.Error())
	}

	if s.RewardTokenSymbol != "" {
		var token assetsModels.CuratedToken
		if err := s.DB.Where("symbol = ?", s.RewardTokenSymbol).First(&token).Error; err == nil {
			rewardRate, rateErr := s.Rates.GetRate(ctx, fiatCurrency, s.RewardTokenSymbol)
			if rateErr == nil {
				rewardAmount := toBaseUnits(rewardFiatAmount.Mul(rewardRate), token.Decimals)
				data, encodeErr := network.EncodeERC20Transfer(user.Address, rewardAmount)
				if encodeErr == nil {
					tokenAddr := common.HexToAddress(token.ContractAddress)
					if _, err := s.Blockchain.SignAndSubmitTx(ctx, faucetKey, &tokenAddr, big.NewInt(0), data, nil); err != nil {
						return apperrors.Internal("failed to dispense activation reward token: " + err.Error())
					}
				}
			}
			// No rate configured for the reward token: gas already sent
			// above, so activation still succeeds - the reward half is
			// simply skipped rather than blocking the whole flow on an
			// operator forgetting to configure a rate.
		}
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&usersModels.User{}).Where("id = ?", user.ID).Update("activated", true).Error; err != nil {
			return err
		}
		return tx.Create(&models.FiatPayment{
			UserID:            user.ID,
			ServiceProvider:   "flutterwave",
			PaymentType:       "ACTIVATION",
			ProviderReference: providerReference,
			Amount:            fiatAmount.InexactFloat64(),
			CreatedAt:         time.Now(),
		}).Error
	})
}

// SettlePendingInvoice submits a PENDING invoice's pre-signed transaction
// (if any) once its fiat payment has cleared, and marks it COMPLETED. This
// is the generic completion half of the decoupled invoice pattern - it
// doesn't know or care what PaymentType the invoice is for, so a future
// asset-purchase flow (Phase 9) reuses it unchanged by creating an invoice
// with SignedTransaction set. A missing invoice (not found, or already
// COMPLETED by an earlier delivery of the same webhook) is a soft no-op,
// matching the original's posture for a duplicate/late webhook delivery.
func (s *Service) SettlePendingInvoice(ctx context.Context, invoiceID, providerReference string, amount float64) error {
	var invoice models.FiatPaymentInvoice
	err := s.DB.Where("id = ? AND status = ?", invoiceID, "PENDING").First(&invoice).Error
	if err != nil {
		return nil
	}
	if invoice.SignedTransaction == nil {
		return nil
	}

	txHash, err := s.Blockchain.SubmitSignedTransaction(ctx, *invoice.SignedTransaction)
	if err != nil {
		// Leave the invoice PENDING - it may still be resubmitted (a
		// duplicated webhook delivery, or a manual retry).
		return apperrors.Internal("failed to submit invoice transaction: " + err.Error())
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&invoice).Updates(map[string]interface{}{"status": "COMPLETED", "transaction_hash": txHash}).Error; err != nil {
			return err
		}
		return tx.Create(&models.FiatPayment{
			UserID:            invoice.UserID,
			ServiceProvider:   invoice.ServiceProvider,
			PaymentType:       invoice.PaymentType,
			ProviderReference: providerReference,
			Amount:            amount,
			CreatedAt:         time.Now(),
		}).Error
	})
}
