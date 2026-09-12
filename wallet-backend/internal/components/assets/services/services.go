package services

import (
	"context"
	"math/big"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"
)

type Service struct {
	DB         *gorm.DB
	Blockchain *network.Client
}

func New(db *gorm.DB, blockchain *network.Client) *Service {
	return &Service{DB: db, Blockchain: blockchain}
}

// ListCurated returns the active entries in the curated-token catalog.
func (s *Service) ListCurated() ([]models.CuratedToken, error) {
	var tokens []models.CuratedToken
	if err := s.DB.Where("is_active = ?", true).Find(&tokens).Error; err != nil {
		return nil, apperrors.Internal("failed to load curated tokens")
	}
	return tokens, nil
}

// Balance returns an address's balance of either native ETH (pass an empty
// tokenAddress) or a specific ERC-20 token, in the smallest unit (wei / the
// token's base unit) as a decimal string.
func (s *Service) Balance(ctx context.Context, address, tokenAddress string) (string, error) {
	if !validators.IsValidAddress(address) {
		return "", apperrors.BadRequest("invalid address")
	}
	if tokenAddress == "" {
		balance, err := s.Blockchain.NativeBalance(ctx, address)
		if err != nil {
			return "", apperrors.Internal("failed to read balance: " + err.Error())
		}
		return balance.String(), nil
	}
	if !validators.IsValidAddress(tokenAddress) {
		return "", apperrors.BadRequest("invalid token contract address")
	}
	balance, err := s.Blockchain.ERC20BalanceOf(ctx, tokenAddress, address)
	if err != nil {
		return "", apperrors.Internal("failed to read balance: " + err.Error())
	}
	return balance.String(), nil
}

// BuildApproveTx returns an unsigned ERC-20 approve(spender, amount)
// transaction for owner to sign client-side - the base template's
// substitute for Stellar's trustline build/submit, see PLAN.md §5.1. amount
// is a decimal string (the token's base unit, not a human-readable amount)
// to avoid floating-point precision loss.
func (s *Service) BuildApproveTx(ctx context.Context, owner, tokenAddress, spender, amount string, nonce *uint64) (*network.UnsignedTx, error) {
	if !validators.IsValidAddress(owner) || !validators.IsValidAddress(spender) {
		return nil, apperrors.BadRequest("invalid address")
	}
	if !validators.IsValidAddress(tokenAddress) {
		return nil, apperrors.BadRequest("invalid token contract address")
	}
	amountWei, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return nil, apperrors.BadRequest("amount must be a decimal integer string in the token's base unit")
	}
	tx, err := s.Blockchain.BuildApproveTx(ctx, owner, tokenAddress, spender, amountWei, nonce)
	if err != nil {
		return nil, apperrors.BadRequest(err.Error())
	}
	return tx, nil
}

// SubmitSignedTransaction submits a client-signed transaction (an approval
// or any other) to the network and returns its hash.
func (s *Service) SubmitSignedTransaction(ctx context.Context, rawTxHex string) (string, error) {
	hash, err := s.Blockchain.SubmitSignedTransaction(ctx, rawTxHex)
	if err != nil {
		return "", apperrors.BadRequest("transaction rejected by the network: " + err.Error())
	}
	return hash, nil
}
