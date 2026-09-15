package services

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/contracts"
	"wallet-backend/internal/cryptoutil"
)

// splitCSV parses a comma-separated username list, trimming whitespace and
// dropping empty entries - upstream's own CSV convention for
// MintingApprovers/MintingInitiators.
func splitCSV(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// canonicalMintMessage is exactly what an approver signs - reconstructed
// identically at verification time, mirroring sharedaccess's
// canonicalActionMessage.
func canonicalMintMessage(approval *models.MintApproval) string {
	return fmt.Sprintf("wallet-backend tokenization mint approval #%d\nasset: %d", approval.ID, approval.TokenizedAssetID)
}

// RequestMint creates (or returns the existing pending) MintApproval for an
// asset at Status FeeAcknowledged. Ported gates: the caller must be a
// global minting approver or initiator, a PUBLIC offering needs a SEC
// approval number and a PRIVATE one needs a ClosedGroupID, the asset must
// have its code/name/description/logo set, and its own MintingApprovers
// CSV must list at least 4 approvers - upstream's exact threshold.
func (s *Service) RequestMint(userID, assetID uint) (*models.MintApproval, error) {
	if !s.IsMintingApprover(userID) && !s.IsMintingInitiator(userID) {
		return nil, apperrors.Forbidden("you are not authorized to initiate minting")
	}
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.Status != models.StatusFeeAcknowledged {
		return nil, apperrors.Conflict("application is not ready to mint")
	}
	if asset.OfferingType == models.OfferingPublic && asset.SecApprovalNumber == "" {
		return nil, apperrors.BadRequest("a SEC approval number is required for a public offering")
	}
	if asset.OfferingType == models.OfferingPrivate && asset.ClosedGroupID == nil {
		return nil, apperrors.BadRequest("a closed group is required for a private offering")
	}
	if asset.AssetCode == "" || asset.AssetName == "" || asset.AssetDescription == "" || asset.AssetLogo == "" {
		return nil, apperrors.BadRequest("assetCode, assetName, assetDescription and assetLogo must all be set before minting")
	}
	approvers := splitCSV(asset.MintingApprovers)
	if len(approvers) < 4 {
		return nil, apperrors.BadRequest("at least 4 minting approvers are required")
	}

	var existing models.MintApproval
	err = s.DB.Where("tokenized_asset_id = ?", assetID).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for an existing mint request")
	}

	approval := models.MintApproval{
		TokenizedAssetID:  assetID,
		RequiredApprovals: len(approvers) - 2,
		Status:            models.MintApprovalPending,
	}
	if err := s.DB.Create(&approval).Error; err != nil {
		return nil, apperrors.Internal("failed to create mint request")
	}
	return &approval, nil
}

// CanonicalMintMessage exposes the exact message an approver must sign.
func (s *Service) CanonicalMintMessage(mintApprovalID uint) (string, error) {
	var approval models.MintApproval
	if err := s.DB.First(&approval, mintApprovalID).Error; err != nil {
		return "", apperrors.NotFound("mint request not found")
	}
	return canonicalMintMessage(&approval), nil
}

// SignMintApproval records one approver's signature and, once the asset's
// own threshold (RequiredApprovals) is met, executes the mint. The signer
// must be an address listed in the asset's MintingApprovers CSV.
func (s *Service) SignMintApproval(ctx context.Context, mintApprovalID uint, approverAddress, signatureHex string) (*models.MintApproval, error) {
	var approval models.MintApproval
	if err := s.DB.First(&approval, mintApprovalID).Error; err != nil {
		return nil, apperrors.NotFound("mint request not found")
	}
	if approval.Status != models.MintApprovalPending {
		return nil, apperrors.Conflict("mint request has already been executed")
	}
	asset, err := s.getAsset(approval.TokenizedAssetID)
	if err != nil {
		return nil, err
	}
	approvers := splitCSV(asset.MintingApprovers)
	authorized := false
	for _, a := range approvers {
		if strings.EqualFold(a, approverAddress) {
			authorized = true
			break
		}
	}
	if !authorized {
		return nil, apperrors.Forbidden("you are not a listed approver for this asset")
	}

	sigBytes := common.FromHex(signatureHex)
	valid, err := cryptoutil.VerifyPersonalSign(canonicalMintMessage(&approval), sigBytes, common.HexToAddress(approverAddress))
	if err != nil || !valid {
		return nil, apperrors.BadRequest("invalid approval signature")
	}

	if err := s.DB.Create(&models.MintApprovalSignoff{
		MintApprovalID:  approval.ID,
		ApproverAddress: approverAddress,
		Signature:       signatureHex,
	}).Error; err != nil {
		return nil, apperrors.Conflict("this approver has already signed")
	}

	var signoffCount int64
	s.DB.Model(&models.MintApprovalSignoff{}).Where("mint_approval_id = ?", approval.ID).Count(&signoffCount)
	if int(signoffCount) < approval.RequiredApprovals {
		return &approval, nil
	}

	if err := s.executeMint(ctx, &approval, asset); err != nil {
		return nil, err
	}
	return &approval, nil
}

// executeMint derives the asset's per-asset issuer/distribution keys,
// deploys TokenizedAsset.sol and Sale.sol, mints the reserved and
// for-sale supply, and moves the asset to Status Minted. See PLAN.md
// §4.9's minting write-up.
func (s *Service) executeMint(ctx context.Context, approval *models.MintApproval, asset *models.TokenizedAsset) error {
	issuerKey, err := s.deriveIssuerKey(asset.ID)
	if err != nil {
		return err
	}
	distributionKey, err := s.deriveDistributionKey(asset.ID)
	if err != nil {
		return err
	}
	issuerAddr := crypto.PubkeyToAddress(issuerKey.PublicKey).Hex()
	distributionAddr := crypto.PubkeyToAddress(distributionKey.PublicKey).Hex()

	// Upstream's checkDistributionWalletHasQuoteCurrencyAuthorization
	// gated a purchase on the distribution wallet already being authorized
	// to hold the internal-balance/quote-currency asset - authorizing it
	// once here at mint time (rather than re-checking on every purchase)
	// matches how authorizeHolder itself is called once per new holder,
	// not once per transfer. A no-op if this country has no internal-
	// balance asset deployed (internal_balance.go, PLAN.md §22.4).
	if err := s.AuthorizeInternalBalanceHolder(ctx, asset.AssetCountryLocation, distributionAddr); err != nil {
		return err
	}

	quoteToken, err := s.curatedToken(asset.AssetQuoteCurrency)
	if err != nil {
		return err
	}

	totalSupply, err := decimal.NewFromString(asset.NumberOfTokenToBeIssued)
	if err != nil {
		return apperrors.BadRequest("invalid numberOfTokenToBeIssued")
	}
	forSale, err := decimal.NewFromString(asset.MaxNumberOfTokenAvailableForSale)
	if err != nil || forSale.GreaterThan(totalSupply) {
		return apperrors.BadRequest("invalid maxNumberOfTokenAvailableForSale")
	}
	reserved := totalSupply.Sub(forSale)
	pricePerToken, err := decimal.NewFromString(asset.PricePerToken)
	if err != nil {
		return apperrors.BadRequest("invalid pricePerToken")
	}

	deployData, err := contracts.TokenizedAssetDeployData(asset.AssetName, asset.AssetCode, asset.AssetDecimals, issuerAddr)
	if err != nil {
		return apperrors.Internal("failed to encode asset contract deployment")
	}
	assetContractAddr, deployTxHash, err := s.Blockchain.DeployContract(ctx, issuerKey, deployData)
	if err != nil {
		return apperrors.Internal("failed to deploy the asset contract: " + err.Error())
	}

	if reserved.GreaterThan(decimal.Zero) {
		mintData, err := contracts.EncodeMint(distributionAddr, toBaseUnits(reserved, asset.AssetDecimals))
		if err != nil {
			return apperrors.Internal("failed to encode reserved mint")
		}
		assetAddr := common.HexToAddress(assetContractAddr)
		if _, err := s.Blockchain.SignAndSubmitTx(ctx, issuerKey, &assetAddr, big.NewInt(0), mintData, nil); err != nil {
			return apperrors.Internal("failed to mint reserved supply: " + err.Error())
		}
	}

	pricePerUnitBaseUnits := toBaseUnits(pricePerToken, quoteToken.Decimals)
	saleDeployData, err := contracts.SaleDeployData(assetContractAddr, quoteToken.ContractAddress, pricePerUnitBaseUnits, distributionAddr, distributionAddr)
	if err != nil {
		return apperrors.Internal("failed to encode sale contract deployment")
	}
	saleContractAddr, _, err := s.Blockchain.DeployContract(ctx, distributionKey, saleDeployData)
	if err != nil {
		return apperrors.Internal("failed to deploy the sale contract: " + err.Error())
	}

	if forSale.GreaterThan(decimal.Zero) {
		mintData, err := contracts.EncodeMint(saleContractAddr, toBaseUnits(forSale, asset.AssetDecimals))
		if err != nil {
			return apperrors.Internal("failed to encode for-sale mint")
		}
		assetAddr := common.HexToAddress(assetContractAddr)
		if _, err := s.Blockchain.SignAndSubmitTx(ctx, issuerKey, &assetAddr, big.NewInt(0), mintData, nil); err != nil {
			return apperrors.Internal("failed to mint sale inventory: " + err.Error())
		}
	}

	if feeAmount, err := decimal.NewFromString(asset.FeeInAsset); err == nil && feeAmount.GreaterThan(decimal.Zero) {
		feeWallet, err := s.FeeWalletAddress()
		if err == nil {
			mintData, encodeErr := contracts.EncodeMint(feeWallet, toBaseUnits(feeAmount, asset.AssetDecimals))
			if encodeErr == nil {
				assetAddr := common.HexToAddress(assetContractAddr)
				_, _ = s.Blockchain.SignAndSubmitTx(ctx, issuerKey, &assetAddr, big.NewInt(0), mintData, nil)
			}
		}
	}

	now := time.Now()
	return s.DB.Transaction(func(tx *gorm.DB) error {
		approval.Status = models.MintApprovalExecuted
		approval.IssuerContractAddress = &assetContractAddr
		approval.SaleContractAddress = &saleContractAddr
		approval.DistributionAddress = &distributionAddr
		approval.MintTxHash = deployTxHash
		if err := tx.Save(approval).Error; err != nil {
			return err
		}
		asset.IssuerContractAddress = &assetContractAddr
		asset.SaleContractAddress = &saleContractAddr
		asset.DistributionAddress = &distributionAddr
		asset.Status = models.StatusMinted
		asset.MintingDate = &now
		if err := tx.Save(asset).Error; err != nil {
			return err
		}
		// Listing the asset as a CuratedToken as soon as it's minted -
		// not gated on PrimarySaleActive - so a primary-sale buyer's
		// balance shows up in their wallet immediately, and so it's
		// tradeable via the market component (Phase 7, curated pairs
		// only) the moment SECONDARY_SALE_ACTIVE is reached. See PLAN.md
		// §4.9's "Secondary sale/trading" note.
		return tx.Create(&assetsModels.CuratedToken{
			Symbol:          asset.AssetCode,
			ContractAddress: assetContractAddr,
			Decimals:        asset.AssetDecimals,
			Name:            asset.AssetName,
			ImageURL:        asset.AssetLogo,
			IsActive:        true,
		}).Error
	})
}

// GetMintApproval fetches a mint request by ID.
func (s *Service) GetMintApproval(mintApprovalID uint) (*models.MintApproval, error) {
	var approval models.MintApproval
	if err := s.DB.First(&approval, mintApprovalID).Error; err != nil {
		return nil, apperrors.NotFound("mint request not found")
	}
	return &approval, nil
}
