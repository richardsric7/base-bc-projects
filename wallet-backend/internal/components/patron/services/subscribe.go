package services

import (
	"context"
	"math/big"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/patron/models"
	"wallet-backend/internal/network"
)

// Quote is what a subscription will cost and when it takes effect -
// returned by BuildSubscription so a caller can show the buyer exactly
// what they're paying and when the upgrade actually applies (immediately,
// or after their current membership's paid-up period ends).
type Quote struct {
	PriceUSD           decimal.Decimal `json:"priceUsd"`
	PaymentAmount      decimal.Decimal `json:"paymentAmount"`
	VATAmount          decimal.Decimal `json:"vatAmount"`
	TotalAmount        decimal.Decimal `json:"totalAmount"`
	PaymentAssetSymbol string          `json:"paymentAssetSymbol"`
	EffectiveDate      time.Time       `json:"effectiveDate"`
	ValidTill          time.Time       `json:"validTill"`
	Instant            bool            `json:"instant"`
}

// schedule computes when a subscription to (packageID, tierID) takes
// effect and how long it's valid, given the buyer's existing membership
// (nil if none) - a direct port of upstream's per-tier effective-date
// branching in SubscribeToPatronPackage.
func schedule(existing *models.UserPatronMembership, packageID, tierID string) (effectiveDate, validTill time.Time, instant bool) {
	now := time.Now()
	if existing == nil {
		return now, tierValidTill(now, tierID), true
	}

	stillValid := existing.ValidTill.After(now)
	existingIsTopTier := existing.ValidTill.Year() == 9999 || existing.PatronTierID == "ANNUAL" || existing.PatronTierID == "LIFETIME"

	if !stillValid {
		// Expired - renew/activate immediately regardless of target tier.
		return now, tierValidTill(now, tierID), true
	}
	if existingIsTopTier {
		return now, tierValidTill(now, tierID), true
	}
	// Existing membership still has time left on a lower tier - queue the
	// upgrade to take effect the day after it expires.
	deferredStart := existing.ValidTill.AddDate(0, 0, 1)
	return deferredStart, tierValidTill(deferredStart, tierID), false
}

func tierValidTill(from time.Time, tierID string) time.Time {
	switch tierID {
	case "LIFETIME":
		return lifetimeDate
	case "ANNUAL":
		return from.AddDate(1, 0, 0)
	case "MONTHLY":
		return from.AddDate(0, 1, 0)
	default:
		return from
	}
}

// quote validates and prices a subscription without persisting anything -
// shared by BuildSubscription (returns it to the caller) and
// ConfirmSubscription (re-derives it fresh rather than trusting
// client-supplied pricing).
func (s *Service) quote(ctx context.Context, userID uint, gradeID uint, paymentAssetSymbol string) (*models.PatronMembershipGrade, *models.UserPatronMembership, *Quote, error) {
	var grade models.PatronMembershipGrade
	if err := s.DB.First(&grade, gradeID).Error; err != nil {
		return nil, nil, nil, apperrors.BadRequest("unknown membership grade")
	}

	var paymentAsset models.PatronSubscriptionPaymentAsset
	if err := s.DB.Where("symbol = ? AND inactive = ?", paymentAssetSymbol, false).First(&paymentAsset).Error; err != nil {
		return nil, nil, nil, apperrors.BadRequest("unsupported payment currency: " + paymentAssetSymbol)
	}

	existing, err := s.GetMembership(userID)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := validateUpgrade(existing, grade.PatronPackageID, grade.PatronTierID); err != nil {
		return nil, nil, nil, err
	}

	var pendingCount int64
	s.DB.Model(&models.UserPatronSubscriptionLog{}).
		Where("user_id = ? AND effective_date > ?", userID, time.Now()).
		Count(&pendingCount)
	if pendingCount > 0 {
		return nil, nil, nil, apperrors.Conflict("you already have a pending subscription")
	}

	effectiveDate, validTill, instant := schedule(existing, grade.PatronPackageID, grade.PatronTierID)

	rate, err := s.Rates.GetRate(ctx, "USD", paymentAssetSymbol)
	if err != nil {
		return nil, nil, nil, apperrors.Internal("no exchange rate configured for USD/" + paymentAssetSymbol)
	}
	priceUSD := decimal.NewFromFloat(grade.PriceUSD)
	paymentAmount := priceUSD.Mul(rate)
	vatAmount := paymentAmount.Mul(decimal.NewFromFloat(s.VATPercent)).Div(decimal.NewFromInt(100))

	return &grade, existing, &Quote{
		PriceUSD:           priceUSD,
		PaymentAmount:      paymentAmount,
		VATAmount:          vatAmount,
		TotalAmount:        paymentAmount.Add(vatAmount),
		PaymentAssetSymbol: paymentAssetSymbol,
		EffectiveDate:      effectiveDate,
		ValidTill:          validTill,
		Instant:            instant,
	}, nil
}

// BuildSubscription validates and prices a subscription, returning the
// unsigned payment transaction for the buyer to sign - nothing is
// persisted until ConfirmSubscription reports a submitted payment.
func (s *Service) BuildSubscription(ctx context.Context, userID uint, gradeID uint, paymentAssetSymbol string) (*network.UnsignedTx, *Quote, error) {
	user, err := s.getUserByID(userID)
	if err != nil {
		return nil, nil, err
	}
	_, _, q, err := s.quote(ctx, userID, gradeID, paymentAssetSymbol)
	if err != nil {
		return nil, nil, err
	}

	feeWallet, err := s.FeeWalletAddress()
	if err != nil {
		return nil, nil, err
	}

	var tx *network.UnsignedTx
	if paymentAssetSymbol == "ETH" {
		tx, err = s.Blockchain.BuildNativeTransferTx(ctx, user.Address, feeWallet, toBaseUnits(q.TotalAmount, 18), nil)
	} else {
		var token assetsModels.CuratedToken
		if dbErr := s.DB.Where("symbol = ? AND is_active = ?", paymentAssetSymbol, true).First(&token).Error; dbErr != nil {
			return nil, nil, apperrors.BadRequest("unsupported payment currency: " + paymentAssetSymbol)
		}
		tx, err = s.Blockchain.BuildERC20TransferTx(ctx, user.Address, token.ContractAddress, feeWallet, toBaseUnits(q.TotalAmount, token.Decimals), nil)
	}
	if err != nil {
		return nil, nil, apperrors.Internal("failed to build subscription payment transaction: " + err.Error())
	}
	return tx, q, nil
}

// ConfirmSubscription re-validates and re-prices the subscription (never
// trusting client-supplied pricing), then records it: a subscription log
// always, and - if the upgrade takes effect immediately - the active
// UserPatronMembership row too, in one transaction. txHash is trusted
// once built, the same posture this codebase takes for every other
// self-submitted on-chain action.
func (s *Service) ConfirmSubscription(ctx context.Context, userID uint, gradeID uint, paymentAssetSymbol, txHash string) (*models.UserPatronSubscriptionLog, error) {
	grade, _, q, err := s.quote(ctx, userID, gradeID, paymentAssetSymbol)
	if err != nil {
		return nil, err
	}

	logEntry := models.UserPatronSubscriptionLog{
		ID:              randomID(),
		UserID:          userID,
		PatronPackageID: grade.PatronPackageID,
		PatronTierID:    grade.PatronTierID,
		EffectiveDate:   q.EffectiveDate,
		ValidTill:       q.ValidTill,
		VATPaid:         q.VATAmount.String(),
		TxHash:          txHash,
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}
		if !q.Instant {
			return nil
		}
		return tx.Save(&models.UserPatronMembership{
			UserID:          userID,
			PatronPackageID: grade.PatronPackageID,
			PatronTierID:    grade.PatronTierID,
			ValidTill:       q.ValidTill,
		}).Error
	})
	if err != nil {
		return nil, apperrors.Internal("failed to record subscription")
	}
	return &logEntry, nil
}

func toBaseUnits(amount decimal.Decimal, decimals uint8) *big.Int {
	scaled := amount.Shift(int32(decimals)).Truncate(0)
	result, ok := new(big.Int).SetString(scaled.String(), 10)
	if !ok {
		return big.NewInt(0)
	}
	return result
}
