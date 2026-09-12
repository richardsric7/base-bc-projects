// Package services implements the crypto component's business logic:
// OneLiquidity-backed external-crypto deposit addresses and withdrawal,
// against OneLiquidity's actual API. See PLAN.md §4.7 for the full design.
package services

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
)

// BlockchainClient is the narrow slice of *network.Client this component
// needs - same narrowing pattern as sharedaccess/kyc/fiat's dependents, so
// deposit crediting and withdrawal-debit submission are unit-testable
// without a live Base RPC.
type BlockchainClient interface {
	SignAndSubmitTx(ctx context.Context, signer *ecdsa.PrivateKey, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (string, error)
	SubmitSignedTransaction(ctx context.Context, rawTxHex string) (string, error)
}

type Service struct {
	DB                *gorm.DB
	Blockchain        BlockchainClient
	HTTPClient        *http.Client
	BaseURL           string
	Token             string
	WalletDomain      string // suffix for the "uid" OneLiquidity subwallets are keyed by: username@WalletDomain
	TreasuryKeySalt   string
	ServiceFeePercent float64
}

func New(db *gorm.DB, blockchain BlockchainClient, baseURL, token, walletDomain, treasuryKeySalt string, serviceFeePercent float64) *Service {
	return &Service{
		DB:                db,
		Blockchain:        blockchain,
		HTTPClient:        &http.Client{Timeout: 15 * time.Second},
		BaseURL:           baseURL,
		Token:             token,
		WalletDomain:      walletDomain,
		TreasuryKeySalt:   treasuryKeySalt,
		ServiceFeePercent: serviceFeePercent,
	}
}

// deriveTreasuryKey derives the single server-controlled key that credits
// deposits and receives withdrawal debits - same try-and-increment
// derivation as the shared-access group keys, the recovery-authority key,
// and the activation faucet key (cryptoutil.DeriveKey). An operator funds
// this one derived address with each curated token deposits should be
// credited from.
func (s *Service) deriveTreasuryKey() (*ecdsa.PrivateKey, error) {
	return cryptoutil.DeriveKey(s.TreasuryKeySalt + "|crypto-treasury")
}

func (s *Service) treasuryAddress() (string, error) {
	key, err := s.deriveTreasuryKey()
	if err != nil {
		return "", err
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

func (s *Service) uidFor(username string) string {
	return username + "@" + s.WalletDomain
}

func isRecordNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}

func (s *Service) doRequest(method, path string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apperrors.Internal("failed to encode OneLiquidity request")
		}
		reqBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, s.BaseURL+path, reqBody)
	if err != nil {
		return apperrors.Internal("failed to build OneLiquidity request")
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return apperrors.Internal("failed to reach OneLiquidity: " + err.Error())
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return apperrors.Internal("failed to read OneLiquidity response")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return apperrors.Internal(fmt.Sprintf("OneLiquidity returned %d: %s", resp.StatusCode, string(respBody)))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return apperrors.Internal("failed to decode OneLiquidity response")
		}
	}
	return nil
}

// findCuratedToken looks up the ERC-20 contract a currency code credits
// as - nil if none is configured, which callers treat as "skip crediting
// for now" rather than an error, matching this port's established
// graceful-degradation posture (see fiat's reward-token skip).
func (s *Service) findCuratedToken(currency string) *assetsModels.CuratedToken {
	var token assetsModels.CuratedToken
	if err := s.DB.Where("symbol = ?", currency).First(&token).Error; err != nil {
		return nil
	}
	return &token
}
