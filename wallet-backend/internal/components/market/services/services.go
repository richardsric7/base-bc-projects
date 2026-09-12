// Package services implements the market component's business logic: an
// off-chain limit-order book the server matches, settled by pulling each
// matched side's asset via transferFrom against a prior approve() rather
// than ever holding user funds - see PLAN.md §4.8 for why (Base has no
// protocol-level DEX order book to place a resting offer on, the way the
// original did against Stellar's).
package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
)

// BlockchainClient is the narrow slice of *network.Client this component
// needs - same narrowing pattern as every other server-signed component in
// this port (sharedaccess, fiat, crypto), so the matching engine's
// settlement calls are unit-testable without a live Base RPC.
type BlockchainClient interface {
	SignAndSubmitTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
}

type Service struct {
	DB            *gorm.DB
	Blockchain    BlockchainClient
	EscrowKeySalt string
}

func New(db *gorm.DB, blockchain BlockchainClient, escrowKeySalt string) *Service {
	return &Service{DB: db, Blockchain: blockchain, EscrowKeySalt: escrowKeySalt}
}

// deriveEscrowKey derives the single server-controlled key that settles
// matched trades via transferFrom - same try-and-increment derivation as
// every other server-key role in this port (shared-access group keys, the
// recovery authority, the activation and crypto-deposit treasuries). A
// maker's approve() of this address is what lets a match settle without
// the server ever holding the asset itself between the two legs.
func (s *Service) deriveEscrowKey() (*ecdsa.PrivateKey, error) {
	return cryptoutil.DeriveKey(s.EscrowKeySalt + "|market-escrow")
}

// EscrowAddress returns the address a maker must approve() before placing
// an offer - exposed so a client can build that approval.
func (s *Service) EscrowAddress() (string, error) {
	key, err := s.deriveEscrowKey()
	if err != nil {
		return "", apperrors.Internal("failed to derive the escrow address")
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), nil
}

func (s *Service) getUserByAddress(address string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("address = ?", address).First(&user).Error; err != nil {
		return nil, apperrors.NotFound("user profile not found - register first")
	}
	return &user, nil
}

// tokenDecimals returns how many decimals tokenAddress uses on-chain - 18
// for native ETH (empty address), or whatever a CuratedToken declares.
// Only curated tokens can be traded, the same restriction the assets
// component already applies to transfers.
func (s *Service) tokenDecimals(tokenAddress string) (uint8, error) {
	if tokenAddress == "" {
		return 18, nil
	}
	var token assetsModels.CuratedToken
	if err := s.DB.Where("contract_address = ?", tokenAddress).First(&token).Error; err != nil {
		return 0, apperrors.BadRequest("token is not curated for trading: " + tokenAddress)
	}
	return token.Decimals, nil
}

func toBaseUnits(amount decimal.Decimal, decimals uint8) *big.Int {
	scaled := amount.Shift(int32(decimals)).Truncate(0)
	result, ok := new(big.Int).SetString(scaled.String(), 10)
	if !ok {
		return big.NewInt(0)
	}
	return result
}

func newTradeID() string {
	return uuid.NewString()
}

func isRecordNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
