package services

import (
	"context"
	"errors"
	"math/big"

	"gorm.io/gorm"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/payments/models"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"
)

type Service struct {
	DB         *gorm.DB
	Blockchain *network.Client
	// Alerts reports a rejected submission to an operational channel -
	// defaults to alerting.NoopNotifier (see New); main.go wires the real
	// one in post-construction, the same pattern users.Service.GeoIP uses,
	// so every existing New(db, blockchain) call site keeps working
	// unchanged. See PLAN.md §4.13.
	Alerts alerting.Notifier
}

func New(db *gorm.DB, blockchain *network.Client) *Service {
	return &Service{DB: db, Blockchain: blockchain, Alerts: alerting.NewNoopNotifier()}
}

// BuildPaymentTx returns an unsigned native-ETH or ERC-20 transfer for the
// source account's owner to sign client-side, with everything needed to
// sign completely offline (see PLAN.md §3). Pass an empty tokenAddress for
// a native ETH transfer. amount is a decimal string in the asset's smallest
// unit (wei for ETH, the token's base unit for an ERC-20) to avoid
// floating-point precision loss.
func (s *Service) BuildPaymentTx(ctx context.Context, from, to, tokenAddress, amount string, nonce *uint64) (*network.UnsignedTx, error) {
	if !validators.IsValidAddress(from) || !validators.IsValidAddress(to) {
		return nil, apperrors.BadRequest("invalid address")
	}
	amountValue, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return nil, apperrors.BadRequest("amount must be a decimal integer string in the asset's smallest unit")
	}

	var tx *network.UnsignedTx
	var err error
	if tokenAddress == "" {
		tx, err = s.Blockchain.BuildNativeTransferTx(ctx, from, to, amountValue, nonce)
	} else {
		if !validators.IsValidAddress(tokenAddress) {
			return nil, apperrors.BadRequest("invalid token contract address")
		}
		tx, err = s.Blockchain.BuildERC20TransferTx(ctx, from, tokenAddress, to, amountValue, nonce)
	}
	if err != nil {
		return nil, apperrors.BadRequest(err.Error())
	}
	return tx, nil
}

// SubmitPayment submits a client-signed payment transaction and records it
// in payment history, keyed by a client-supplied idempotency key. Because
// signing can happen long after Build (see PLAN.md §3 - the whole point of
// offline signing), an app's natural retry-on-reconnect behavior means the
// same signed transaction may arrive here more than once; resubmitting the
// same idempotencyKey returns the original record rather than erroring or
// creating a duplicate history entry.
func (s *Service) SubmitPayment(ctx context.Context, idempotencyKey, signedTx, fromAddress, toAddress, tokenAddress, amount string) (*models.PaymentHistory, error) {
	var existing models.PaymentHistory
	err := s.DB.Where("idempotency_key = ?", idempotencyKey).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for a previous submission")
	}

	hash, err := s.Blockchain.SubmitSignedTransaction(ctx, signedTx)
	if err != nil {
		// Best-effort operational alert, matching the original's behavior
		// of alerting on every rejected submission rather than trying to
		// distinguish user error (bad nonce, insufficient balance) from an
		// infra failure (RPC unreachable) - see PLAN.md §4.13.
		_ = s.Alerts.Notify("payment submission rejected by the network: " + err.Error())
		return nil, apperrors.BadRequest("transaction rejected by the network: " + err.Error())
	}

	record := models.PaymentHistory{
		IdempotencyKey: idempotencyKey,
		FromAddress:    fromAddress,
		ToAddress:      toAddress,
		TokenAddress:   tokenAddress,
		Amount:         amount,
		TxHash:         hash,
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return nil, apperrors.Internal("payment submitted but failed to record history")
	}
	return &record, nil
}

// History returns the payments an address has sent or received, most
// recent first.
func (s *Service) History(address string) ([]models.PaymentHistory, error) {
	var history []models.PaymentHistory
	err := s.DB.Where("from_address = ? OR to_address = ?", address, address).
		Order("created_at DESC").
		Find(&history).Error
	if err != nil {
		return nil, apperrors.Internal("failed to load payment history")
	}
	return history, nil
}
