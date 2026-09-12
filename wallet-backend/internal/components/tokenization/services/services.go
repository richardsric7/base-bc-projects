// Package services implements tokenization's business logic - the
// original's largest subsystem. See PLAN.md §4.9 for the full design:
// what ports directly (reference data, the status state machine, the
// minting-gate CSV multisig, NAV-based early-exit penalty math), what's
// substituted (a deployed ERC-20 + Sale.sol contract pair instead of a
// Stellar issuer account and standing DEX offer), and what's simplified
// or dropped (no trustline/opt-in, no intermediate quote currency, no
// path-payment pathfinding, no bespoke secondary-market code - Phase 7's
// market component already trades any curated pair).
package services

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/tokenization/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
	"wallet-backend/internal/storage"
)

// CreateFiatInvoiceFunc mirrors fiat/services.Service.CreateInvoice's
// signature, minus the returned invoice (tokenization only needs to know
// whether creation succeeded - it already generated the invoice/
// subscription ID itself). Wired as a post-construction callback from
// main.go rather than an import of internal/components/fiat/services
// directly, the same pattern kyc's OnBVNVerified hook uses to reach
// stablerail without a cross-component import.
type CreateFiatInvoiceFunc func(address, id, serviceProvider, paymentType string, amount float64, currency string, signedTransaction *string) error

// BlockchainClient is the narrow slice of *network.Client this component
// needs - same narrowing pattern as every other component's dependents,
// so minting/sale/early-exit paths are unit-testable without a live Base
// RPC. DeployContract and BuildContractCallTx cover, respectively, the
// two on-chain actions this component itself signs (contract deployment
// and server-side fiat-purchase transfers) and the ones it only builds
// for a holder to sign themselves (buy/burn).
type BlockchainClient interface {
	DeployContract(ctx context.Context, deployer *ecdsa.PrivateKey, data []byte) (contractAddress, txHash string, err error)
	SignAndSubmitTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
	SignTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
	BuildContractCallTx(ctx context.Context, from, contractAddress string, value *big.Int, data []byte, explicitNonce *uint64) (*network.UnsignedTx, error)
	SubmitSignedTransaction(ctx context.Context, rawTxHex string) (string, error)
	ERC20BalanceOf(ctx context.Context, tokenAddress, owner string) (*big.Int, error)
}

type Service struct {
	DB         *gorm.DB
	Blockchain BlockchainClient
	Storage    storage.Blob
	// CreateFiatInvoice is nil unless main.go wires it (a fiat processor is
	// configured) - see subscribe_fiat.go, which rejects the fiat purchase
	// path when nil, same posture as every other optional-vendor path in
	// this port.
	CreateFiatInvoice   CreateFiatInvoiceFunc
	IssuerKeySalt       string // seed for cryptoutil.DeriveKey(salt+"|tokenization-issuer|"+assetID)
	DistributionKeySalt string // seed for cryptoutil.DeriveKey(salt+"|tokenization-distribution|"+assetID)
	TokenLimit          decimal.Decimal
}

func New(db *gorm.DB, blockchain BlockchainClient, blob storage.Blob, issuerKeySalt, distributionKeySalt string, tokenLimit decimal.Decimal) *Service {
	return &Service{
		DB:                  db,
		Blockchain:          blockchain,
		Storage:             blob,
		IssuerKeySalt:       issuerKeySalt,
		DistributionKeySalt: distributionKeySalt,
		TokenLimit:          tokenLimit,
	}
}

func (s *Service) deriveIssuerKey(assetID uint) (*ecdsa.PrivateKey, error) {
	key, err := cryptoutil.DeriveKey(s.IssuerKeySalt + "|tokenization-issuer|" + uintToString(assetID))
	if err != nil {
		return nil, apperrors.Internal("failed to derive the asset's issuer key")
	}
	return key, nil
}

func (s *Service) deriveDistributionKey(assetID uint) (*ecdsa.PrivateKey, error) {
	key, err := cryptoutil.DeriveKey(s.DistributionKeySalt + "|tokenization-distribution|" + uintToString(assetID))
	if err != nil {
		return nil, apperrors.Internal("failed to derive the asset's distribution key")
	}
	return key, nil
}

func (s *Service) getUserByID(userID uint) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user not found")
		}
		return nil, apperrors.Internal("failed to load user")
	}
	return &user, nil
}

// ResolveUserID maps a verified session address (the JWT subject every
// controller handler starts from, per this codebase's convention) to the
// users.User.ID this component's services key everything on internally.
func (s *Service) ResolveUserID(address string) (uint, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return 0, err
	}
	return user.ID, nil
}

func (s *Service) getUserByAddress(address string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("address = ?", address).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("user not found")
		}
		return nil, apperrors.Internal("failed to load user")
	}
	return &user, nil
}

// getAsset fetches a TokenizedAsset by ID.
func (s *Service) getAsset(assetID uint) (*models.TokenizedAsset, error) {
	var asset models.TokenizedAsset
	if err := s.DB.First(&asset, assetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("tokenized asset not found")
		}
		return nil, apperrors.Internal("failed to load tokenized asset")
	}
	return &asset, nil
}

// curatedToken resolves a CuratedToken by symbol - used everywhere a quote
// currency, payout currency, or application-fee asset needs its contract
// address/decimals. Symbol lookups are case-sensitive by design (curated
// symbols are seeded in uppercase, matching the assets component).
func (s *Service) curatedToken(symbol string) (*assetsModels.CuratedToken, error) {
	var token assetsModels.CuratedToken
	if err := s.DB.Where("symbol = ? AND is_active = ?", symbol, true).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.BadRequest("unsupported currency: " + symbol)
		}
		return nil, apperrors.Internal("failed to resolve currency")
	}
	return &token, nil
}

// isAllowedTokenizationCurrency checks a symbol against the tokenization
// component's own allow-list (models.TokenizationCurrency) - a narrower
// set than "every curated token", since not every wallet-supported asset
// is meant to be usable as a purchase/payout currency here.
func (s *Service) isAllowedTokenizationCurrency(symbol string) bool {
	var count int64
	s.DB.Model(&models.TokenizationCurrency{}).Where("symbol = ?", symbol).Count(&count)
	return count > 0
}

// toBaseUnits/fromBaseUnits convert between a human decimal quantity and
// on-chain base units, truncating any precision beyond the token's
// decimals - the same helper every other component with an ERC-20 amount
// re-implements locally (see fiat.toBaseUnits, crypto's equivalent).
func toBaseUnits(amount decimal.Decimal, decimals uint8) *big.Int {
	scaled := amount.Shift(int32(decimals)).Truncate(0)
	result, ok := new(big.Int).SetString(scaled.String(), 10)
	if !ok {
		return big.NewInt(0)
	}
	return result
}

func fromBaseUnits(amount *big.Int, decimals uint8) decimal.Decimal {
	return decimal.NewFromBigInt(amount, 0).Shift(-int32(decimals))
}

func uintToString(v uint) string {
	return decimal.NewFromInt(int64(v)).String()
}
