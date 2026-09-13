package services

import (
	"context"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/contracts"
	"wallet-backend/internal/network"
)

// parseExitPercentage parses a free-text percentage field (e.g. "5%" or
// "5"), returning 0 for anything unset or unparseable - matching
// upstream's own permissive parseEarlyExitPercentage.
func parseExitPercentage(value string) decimal.Decimal {
	value = strings.TrimSuffix(strings.TrimSpace(value), "%")
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return decimal.Zero
	}
	return decimal.NewFromFloat(parsed)
}

// BuildEarlyExit builds the unsigned burn(quantity) call for the holder to
// sign themselves - TokenizedAsset.sol is ERC20Burnable, so no
// server-signed "payment back to the distribution wallet" transaction is
// needed at all, unlike upstream (a simplification, not a feature gap -
// see PLAN.md §4.9). Ported gates: the asset must be open for trading, and
// its maturity date (if any) must not have already passed.
func (s *Service) BuildEarlyExit(ctx context.Context, holderUserID, assetID uint, quantity decimal.Decimal) (*network.UnsignedTx, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.IssuerContractAddress == nil {
		return nil, apperrors.Conflict("this asset has not been minted yet")
	}
	if asset.Status != models.StatusPrimarySaleActive && asset.Status != models.StatusSecondarySaleActive {
		return nil, apperrors.Conflict("this asset is not currently open for early exit")
	}
	if asset.MaturityDate != nil && time.Now().After(*asset.MaturityDate) {
		return nil, apperrors.Conflict("this asset has matured - use standard redemption instead of early exit")
	}
	if quantity.LessThanOrEqual(decimal.Zero) {
		return nil, apperrors.BadRequest("tokenQuantityToExit must be positive")
	}

	holder, err := s.getUserByID(holderUserID)
	if err != nil {
		return nil, err
	}

	data, err := contracts.EncodeBurn(toBaseUnits(quantity, asset.AssetDecimals))
	if err != nil {
		return nil, apperrors.Internal("failed to encode burn")
	}
	return s.Blockchain.BuildContractCallTx(ctx, holder.Address, *asset.IssuerContractAddress, big.NewInt(0), data, nil)
}

// RecordEarlyExit records a self-submitted burn as an early exit,
// computing the same NAV-based penalty payout formula upstream used:
// payoutPricePerToken = (CurrentNAVPerToken or PricePerToken) * (1 -
// (EarlyExitPenaltyPercent + EarlyExitFeePercent)/100). The payout itself
// is settled off-chain/manually against the given bank details, exactly
// as upstream never automated it on-chain either.
func (s *Service) RecordEarlyExit(holderUserID, assetID uint, quantity decimal.Decimal, bankID uint, accountNumber, accountName, burnTxHash string) (*models.TokenizedAssetEarlyExit, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	holder, err := s.getUserByID(holderUserID)
	if err != nil {
		return nil, err
	}

	navPerToken, err := decimal.NewFromString(asset.CurrentNAVPerToken)
	if err != nil || navPerToken.LessThanOrEqual(decimal.Zero) {
		navPerToken, err = decimal.NewFromString(asset.PricePerToken)
		if err != nil {
			return nil, apperrors.Internal("this asset has no valid NAV or price per token")
		}
	}
	penaltyPercent := parseExitPercentage(asset.EarlyExitPenaltyPercent).Add(parseExitPercentage(asset.EarlyExitFeePercent))
	payoutPricePerToken := navPerToken.Mul(decimal.NewFromInt(1).Sub(penaltyPercent.Div(decimal.NewFromInt(100))))
	estimatedPayout := payoutPricePerToken.Mul(quantity)

	exit := models.TokenizedAssetEarlyExit{
		TokenizedAssetID:      assetID,
		HolderUserID:          holderUserID,
		HolderAddress:         holder.Address,
		BankID:                bankID,
		AccountNumber:         accountNumber,
		AccountName:           accountName,
		PayoutCurrency:        asset.AssetQuoteCurrency,
		BurnTxHash:            burnTxHash,
		TokenQuantityExited:   quantity.String(),
		NAVPerTokenAtExit:     navPerToken.String(),
		PenaltyPercentApplied: penaltyPercent.String(),
		PayoutPricePerToken:   payoutPricePerToken.String(),
		EstimatedPayoutAmount: estimatedPayout.String(),
	}
	if err := s.DB.Create(&exit).Error; err != nil {
		return nil, apperrors.Internal("failed to record early exit")
	}
	return &exit, nil
}

// ListMyEarlyExits lists one holder's own early-exit records.
func (s *Service) ListMyEarlyExits(holderUserID uint) ([]models.TokenizedAssetEarlyExit, error) {
	var exits []models.TokenizedAssetEarlyExit
	if err := s.DB.Where("holder_user_id = ?", holderUserID).Order("created_at DESC").Find(&exits).Error; err != nil {
		return nil, apperrors.Internal("failed to load early exits")
	}
	return exits, nil
}
