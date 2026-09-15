package services

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/contracts"
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

// BuildEarlyExit proposes the burn(quantity) call as a real Safe
// transaction against the holder's own primary wallet and returns the
// digest signerAddress must personal_sign to approve it - TokenizedAsset.sol
// is ERC20Burnable, so no server-signed "payment back to the distribution
// wallet" transaction is needed at all, unlike upstream (a simplification,
// not a feature gap - see PLAN.md §4.9). This can no longer build a plain
// unsigned transaction the way it originally did - see
// GroupWalletExecutor's doc comment. Ported gates: the asset must be open
// for trading, and its maturity date (if any) must not have already
// passed.
func (s *Service) BuildEarlyExit(ctx context.Context, holderUserID, assetID uint, quantity decimal.Decimal, signerAddress string) (*PurchaseProposal, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("early exit is not available: shared-access wiring is missing")
	}
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
	group, err := s.SharedAccess.GetGroupByAddress(holder.Address)
	if err != nil {
		return nil, err
	}
	dataHex := "0x" + common.Bytes2Hex(data)
	action, err := s.SharedAccess.ProposeContractCall(ctx, signerAddress, group.ID, sharedaccessModels.ActionContractCall, "tokenization early exit", *asset.IssuerContractAddress, "0", dataHex, "tokenization-early-exit", uintToString(assetID))
	if err != nil {
		return nil, err
	}
	digest, err := s.SharedAccess.DigestToSign(action.ID, signerAddress)
	if err != nil {
		return nil, err
	}
	return &PurchaseProposal{ActionID: action.ID, DigestToSign: digest}, nil
}

// ConfirmEarlyExit approves actionID (proposed via BuildEarlyExit) with
// signerAddress's personal_sign signature over its digest, executing the
// burn immediately once the group's approval threshold is met, then
// records the early exit using the real on-chain transaction hash from
// the executed action - computing the same NAV-based penalty payout
// formula upstream used: payoutPricePerToken = (CurrentNAVPerToken or
// PricePerToken) * (1 - (EarlyExitPenaltyPercent +
// EarlyExitFeePercent)/100). The payout itself is settled off-chain/
// manually against the given bank details, exactly as upstream never
// automated it on-chain either.
func (s *Service) ConfirmEarlyExit(ctx context.Context, holderUserID, assetID uint, quantity decimal.Decimal, bankID uint, accountNumber, accountName string, actionID uint, signerAddress, signature string) (*models.TokenizedAssetEarlyExit, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("early exit is not available: shared-access wiring is missing")
	}
	action, err := s.SharedAccess.ApproveAction(ctx, actionID, signerAddress, signature)
	if err != nil {
		return nil, err
	}
	if action.Status != sharedaccessModels.ActionExecuted {
		return nil, apperrors.BadRequest("early exit is not yet executed (status: " + string(action.Status) + ") - approve again once outstanding approvals are collected")
	}

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
		BurnTxHash:            action.TxHash,
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
