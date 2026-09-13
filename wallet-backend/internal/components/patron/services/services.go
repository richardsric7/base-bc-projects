// Package services implements patron/membership subscriptions - fully
// chain-agnostic tier/package upgrade-downgrade business logic ported
// directly from upstream, with only the payment leg changed (a plain
// native/ERC-20 transfer to a derived fee wallet instead of a Stellar
// path-payment-through-TROV). See PLAN.md §4.10.
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/patron/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
	"wallet-backend/internal/rates"
)

// BlockchainClient is the narrow slice of *network.Client this component
// needs - same narrowing pattern as every other component's dependents.
type BlockchainClient interface {
	BuildNativeTransferTx(ctx context.Context, from, to string, amountWei *big.Int, explicitNonce *uint64) (*network.UnsignedTx, error)
	BuildERC20TransferTx(ctx context.Context, from, tokenAddress, to string, amount *big.Int, explicitNonce *uint64) (*network.UnsignedTx, error)
}

type Service struct {
	DB            *gorm.DB
	Blockchain    BlockchainClient
	Rates         rates.Provider
	FeeWalletSalt string
	VATPercent    float64
}

func New(db *gorm.DB, blockchain BlockchainClient, ratesProvider rates.Provider, feeWalletSalt string, vatPercent float64) *Service {
	return &Service{DB: db, Blockchain: blockchain, Rates: ratesProvider, FeeWalletSalt: feeWalletSalt, VATPercent: vatPercent}
}

// FeeWalletAddress is the single derived address patron subscription fees
// are paid to - a cryptoutil.DeriveKey-derived address rather than a
// configured raw address, matching every other server-controlled
// fee-collection role in this port.
func (s *Service) FeeWalletAddress() (string, error) {
	key, err := cryptoutil.DeriveKey(s.FeeWalletSalt + "|patron-fee-wallet")
	if err != nil {
		return "", apperrors.Internal("failed to derive the patron fee wallet address")
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), nil
}

// ResolveUserID maps a verified session address (the JWT subject every
// controller handler starts from) to the users.User.ID this component's
// services key everything on internally - same pattern as tokenization's
// ResolveUserID.
func (s *Service) ResolveUserID(address string) (uint, error) {
	var user usersModels.User
	if err := s.DB.Where("address = ?", address).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, apperrors.NotFound("user not found")
		}
		return 0, apperrors.Internal("failed to load user")
	}
	return user.ID, nil
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

// ReferenceData bundles everything the subscription form needs in one call.
type ReferenceData struct {
	Packages      []models.PatronPackage                  `json:"packages"`
	Tiers         []models.PatronTier                     `json:"tiers"`
	Grades        []models.PatronMembershipGrade          `json:"grades"`
	PaymentAssets []models.PatronSubscriptionPaymentAsset `json:"paymentAssets"`
}

func (s *Service) ReferenceData() (*ReferenceData, error) {
	var data ReferenceData
	if err := s.DB.Order("priority_order ASC").Where("inactive = ?", false).Find(&data.Packages).Error; err != nil {
		return nil, apperrors.Internal("failed to load patron packages")
	}
	if err := s.DB.Order("priority_order ASC").Where("inactive = ?", false).Find(&data.Tiers).Error; err != nil {
		return nil, apperrors.Internal("failed to load patron tiers")
	}
	if err := s.DB.Find(&data.Grades).Error; err != nil {
		return nil, apperrors.Internal("failed to load patron membership grades")
	}
	if err := s.DB.Where("inactive = ?", false).Find(&data.PaymentAssets).Error; err != nil {
		return nil, apperrors.Internal("failed to load patron payment assets")
	}
	return &data, nil
}

// GetMembership fetches a user's current membership, if any.
func (s *Service) GetMembership(userID uint) (*models.UserPatronMembership, error) {
	var membership models.UserPatronMembership
	err := s.DB.Where("user_id = ?", userID).First(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.Internal("failed to load membership")
	}
	return &membership, nil
}

// GetSubscriptionLogs lists a user's subscription history, newest first.
func (s *Service) GetSubscriptionLogs(userID uint) ([]models.UserPatronSubscriptionLog, error) {
	var logs []models.UserPatronSubscriptionLog
	if err := s.DB.Where("user_id = ?", userID).Order("created_at DESC").Find(&logs).Error; err != nil {
		return nil, apperrors.Internal("failed to load subscription history")
	}
	return logs, nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var lifetimeDate = time.Date(9999, 12, 1, 23, 59, 59, 0, time.UTC)

// validateUpgrade ports upstream's exact qualification rules: you cannot
// move to a grade that is not a genuine upgrade over an existing,
// still-valid membership at the top of its package's tier ladder.
func validateUpgrade(existing *models.UserPatronMembership, targetPackageID, targetTierID string) error {
	if existing == nil {
		return nil
	}
	if existing.ValidTill.Before(time.Now()) {
		// Expired - any grade is a valid renewal/fresh start.
		return nil
	}
	switch existing.PatronPackageID {
	case "DIAMOND":
		if existing.PatronTierID == "LIFETIME" {
			return apperrors.Conflict("account is already a member of the highest available tier and package")
		}
	case "PLATINUM":
		if existing.PatronTierID == "LIFETIME" && (targetPackageID == "GOLD" || targetPackageID == "PLATINUM") {
			return apperrors.Conflict("account is already a member of the highest available tier in this package")
		}
	case "GOLD":
		if existing.PatronTierID == "LIFETIME" && targetPackageID == "GOLD" {
			return apperrors.Conflict("account is already a member of the highest available tier in this package")
		}
	}
	return nil
}
