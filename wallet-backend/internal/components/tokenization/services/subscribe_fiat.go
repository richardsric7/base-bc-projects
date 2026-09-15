package services

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/network"
)

// BuildFiatPurchase is the fiat purchase flow's build phase. Unlike the
// crypto path, settlement is a server-derived distribution-key transfer
// rather than something the buyer must co-sign - Base needs no trustline
// step to require a buyer signature for, unlike upstream. So the server
// signs the transfer now (network.SignTx, not SignAndSubmitTx) and hands
// the raw signed transaction to CreateFiatInvoice, reusing internal/fiat's
// decoupled invoice pattern completely unchanged: the existing Flutterwave
// webhook's generic default branch calls SettlePendingInvoice to submit it
// once payment clears. See PLAN.md §4.9's "Primary sale (fiat)" note.
func (s *Service) BuildFiatPurchase(ctx context.Context, buyerUserID, assetID uint, quantity decimal.Decimal, invoiceID, fiatCurrency string) (*models.TokenizedAssetSubscription, error) {
	if s.CreateFiatInvoice == nil {
		return nil, apperrors.BadRequest("fiat purchases are not enabled")
	}
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.DistributionAddress == nil {
		return nil, apperrors.Conflict("this asset has not been minted yet")
	}
	if quantity.LessThanOrEqual(decimal.Zero) {
		return nil, apperrors.BadRequest("quantity must be positive")
	}

	pricePerToken, err := decimal.NewFromString(asset.PricePerToken)
	if err != nil {
		return nil, apperrors.Internal("invalid pricePerToken on this asset")
	}
	paymentAmount := quantity.Mul(pricePerToken)
	if err := s.enforceCapAndStatus(asset, buyerUserID, quantity, paymentAmount); err != nil {
		return nil, err
	}

	buyer, err := s.getUserByID(buyerUserID)
	if err != nil {
		return nil, err
	}
	if err := s.requireBuyerKYC(buyer); err != nil {
		return nil, err
	}
	if err := s.enforceOfferingAccess(asset, buyer.Address); err != nil {
		return nil, err
	}
	// See BuildCryptoPurchase's identical call for why: the buyer's wallet
	// must be authorized to hold this restricted asset before the
	// distribution-key transfer below can ever land (it's pre-signed now
	// but only submitted once payment clears - authorizing immediately,
	// rather than waiting for settlement, means it's already true well
	// before that submission happens).
	if err := s.authorizeHolder(ctx, asset, buyer.Address); err != nil {
		return nil, err
	}
	// See BuildCryptoPurchase's identical call: the fiat-denominated
	// purchase amount is quoted in this country's internal-balance asset
	// (PLAN.md §22.4), itself restricted - mirrors upstream's
	// checkDistributionWalletHasQuoteCurrencyAuthorization. A no-op for a
	// country with no internal-balance asset deployed.
	if err := s.AuthorizeInternalBalanceHolder(ctx, asset.AssetCountryLocation, buyer.Address); err != nil {
		return nil, err
	}

	distributionKey, err := s.deriveDistributionKey(assetID)
	if err != nil {
		return nil, err
	}
	transferData, err := network.EncodeERC20Transfer(buyer.Address, toBaseUnits(quantity, asset.AssetDecimals))
	if err != nil {
		return nil, apperrors.Internal("failed to encode purchase transfer")
	}
	assetAddr := common.HexToAddress(*asset.IssuerContractAddress)
	rawSignedTx, err := s.Blockchain.SignTx(ctx, distributionKey, &assetAddr, nil, transferData, nil)
	if err != nil {
		return nil, apperrors.Internal("failed to sign purchase transfer: " + err.Error())
	}

	sub := models.TokenizedAssetSubscription{
		ID:                 invoiceID,
		TokenizedAssetID:   assetID,
		SubscriberUserID:   buyerUserID,
		SubscriberAddress:  buyer.Address,
		Quantity:           quantity.String(),
		PaymentAmount:      paymentAmount.String(),
		PaymentAssetSymbol: asset.AssetQuoteCurrency,
		Channel:            "FIAT",
	}
	if err := s.DB.Create(&sub).Error; err != nil {
		return nil, apperrors.Conflict("a purchase with this reference already exists")
	}

	if err := s.CreateFiatInvoice(buyer.Address, invoiceID, "flutterwave", "TOKENIZED_ASSET_PURCHASE", paymentAmount.InexactFloat64(), fiatCurrency, &rawSignedTx); err != nil {
		s.DB.Delete(&sub)
		return nil, apperrors.Internal("failed to create fiat invoice: " + err.Error())
	}
	return &sub, nil
}
