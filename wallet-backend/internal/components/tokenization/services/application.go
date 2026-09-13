package services

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/network"
)

// resetServerControlledFields overwrites every field this service (not the
// applicant) owns, regardless of what the client's JSON body included -
// identifiers, lifecycle state, vetting-computed fee snapshots, and
// minting/contract addresses are never applicant input. Called on every
// SubmitApplication so a resubmission can never smuggle a value into one
// of these fields.
func resetServerControlledFields(body *models.TokenizedAsset, userID uint) {
	body.InitiatorUserID = userID
	body.Status = models.StatusDraft
	body.VettingStatus = false
	body.DueDiligenceFailed = false
	body.DueDiligenceFailureReason = ""
	body.IssuerContractAddress = nil
	body.SaleContractAddress = nil
	body.DistributionAddress = nil
	body.ApprovedAssetCustodianID = 0
	body.AssetManagerID = 0
	body.AssetIssuingHouseID = 0
	body.LegalAndProfessionalPartnerID = 0
	body.RatingAgencyID = 0
	body.TrusteeID = 0
	body.LegalAdviserID = 0
	body.FinancialAdviserID = 0
	body.MintingInitiators = ""
	body.MintingApprovers = ""
	body.SECFeePercent, body.SECFeeFixed, body.SECFeeValue = "", "", ""
	body.CustodianFeePercent, body.CustodianFeeFixed, body.CustodianFeeValue = "", "", ""
	body.AssetManagerFeePercent, body.AssetManagerFeeFixed, body.AssetManagerFeeValue = "", "", ""
	body.IssuingHouseFeePercent, body.IssuingHouseFeeFixed, body.IssuingHouseFeeValue = "", "", ""
	body.LegalPartnerFeePercent, body.LegalPartnerFeeFixed, body.LegalPartnerFeeValue = "", "", ""
	body.RatingAgencyFeePercent, body.RatingAgencyFeeFixed, body.RatingAgencyFeeValue = "", "", ""
	body.TrusteeFeePercent, body.TrusteeFeeFixed, body.TrusteeFeeValue = "", "", ""
	body.VATPercent, body.VATValue, body.VATInAsset = "", "", ""
	body.LastUpdatedBy = ""
}

// findOpenApplication returns this user's most recent non-deleted
// application, if one exists - used to enforce "one open draft at a time"
// and to find the row a resubmission should update rather than duplicate.
func (s *Service) findOpenApplication(userID uint) (*models.TokenizedAsset, error) {
	var asset models.TokenizedAsset
	err := s.DB.Where("initiator_user_id = ?", userID).Order("created_at DESC").First(&asset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.Internal("failed to load existing application")
	}
	return &asset, nil
}

// SubmitApplication creates or updates a Draft application. Ported
// validations: the applicant must be KYC-verified, the requested supply
// must not exceed the global token limit, and a resubmission is only
// allowed while the existing application is still at Status Draft ("still
// being edited"). See PLAN.md §4.9's "Quote currency" note for why this
// no longer needs a CountryConfig-resolved internal-balance-token lookup
// the way upstream's equivalent did.
func (s *Service) SubmitApplication(userID uint, body *models.TokenizedAsset) (*models.TokenizedAsset, error) {
	user, err := s.getUserByID(userID)
	if err != nil {
		return nil, err
	}
	if user.KYCVerifiedLevel == 0 {
		return nil, apperrors.Forbidden("KYC verification is required before applying to tokenize an asset")
	}

	supply, err := decimal.NewFromString(body.NumberOfTokenToBeIssued)
	if err != nil || supply.LessThanOrEqual(decimal.Zero) {
		return nil, apperrors.BadRequest("numberOfTokenToBeIssued must be a positive number")
	}
	if s.TokenLimit.GreaterThan(decimal.Zero) && supply.GreaterThan(s.TokenLimit) {
		return nil, apperrors.BadRequest("numberOfTokenToBeIssued exceeds the maximum allowed")
	}

	if len(body.AssetCountryLocation) != 2 {
		return nil, apperrors.BadRequest("assetCountryLocation must be a 2-letter country code")
	}
	var countryConfig models.TokenizationCountryConfig
	if err := s.DB.Where("country_code = ?", body.AssetCountryLocation).First(&countryConfig).Error; err != nil {
		return nil, apperrors.BadRequest("no tokenization configuration exists for this country")
	}

	if body.AssetQuoteCurrency == "" {
		body.AssetQuoteCurrency = "USDC"
	}
	if !s.isAllowedTokenizationCurrency(body.AssetQuoteCurrency) {
		return nil, apperrors.BadRequest("unsupported quote currency: " + body.AssetQuoteCurrency)
	}
	if body.ProceedPayoutCurrency == "" {
		body.ProceedPayoutCurrency = "CNGN"
	}
	if !s.isAllowedTokenizationCurrency(body.ProceedPayoutCurrency) {
		return nil, apperrors.BadRequest("unsupported proceed payout currency: " + body.ProceedPayoutCurrency)
	}

	existing, err := s.findOpenApplication(userID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Status != models.StatusDraft {
		return nil, apperrors.Conflict("your existing application has moved past the draft stage and can no longer be edited here")
	}

	resetServerControlledFields(body, userID)
	if existing != nil {
		body.ID = existing.ID
		body.CreatedAt = existing.CreatedAt
	}
	body.UpdatedAt = time.Now()

	if err := s.DB.Save(body).Error; err != nil {
		return nil, apperrors.Internal("failed to save application")
	}
	return body, nil
}

// GetByID fetches one application - callers enforce their own
// ownership/visibility rules (see controllers).
func (s *Service) GetByID(assetID uint) (*models.TokenizedAsset, error) {
	return s.getAsset(assetID)
}

// ListByInitiator lists an initiator's own applications, newest first.
func (s *Service) ListByInitiator(userID uint) ([]models.TokenizedAsset, error) {
	var assets []models.TokenizedAsset
	if err := s.DB.Where("initiator_user_id = ?", userID).Order("created_at DESC").Find(&assets).Error; err != nil {
		return nil, apperrors.Internal("failed to load applications")
	}
	return assets, nil
}

// ConfirmApplication moves Draft → ApplicationConfirmed and charges the
// application fee: a plain ERC-20/native transfer from the applicant's own
// address to the derived fee-collection address, replacing upstream's
// single Payment operation. Requires a TokenizationFeeID (upstream's own
// gate). Returns the unsigned transaction for the applicant to sign - if
// signedTransaction is already provided, it's submitted immediately in the
// same call (mirroring upstream's "confirm and submit in one request"
// behavior when a signature is already in hand).
func (s *Service) ConfirmApplication(ctx context.Context, userID, assetID uint, signedTransaction *string) (*models.TokenizedAsset, *network.UnsignedTx, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, nil, err
	}
	if asset.InitiatorUserID != userID {
		return nil, nil, apperrors.Forbidden("you may only confirm your own application")
	}
	if asset.Status != models.StatusDraft {
		return nil, nil, apperrors.Conflict("application is not in draft status")
	}
	if asset.TokenizationFeeID == nil || *asset.TokenizationFeeID == 0 {
		return nil, nil, apperrors.BadRequest("select a tokenization fee before confirming the application")
	}

	user, err := s.getUserByID(userID)
	if err != nil {
		return nil, nil, err
	}

	feeAsset := asset.TokenizationApplicationFeeAsset
	if feeAsset == "" {
		feeAsset = "TROV"
	}
	feeWallet, err := s.FeeWalletAddress()
	if err != nil {
		return nil, nil, err
	}
	feeAmount, err := decimal.NewFromString(asset.TokenizationApplicationFee)
	if err != nil {
		feeAmount = decimal.Zero
	}

	var unsignedTx *network.UnsignedTx
	if feeAmount.GreaterThan(decimal.Zero) {
		token, err := s.curatedToken(feeAsset)
		if err != nil {
			return nil, nil, err
		}
		amount := toBaseUnits(feeAmount, token.Decimals)
		data, err := network.EncodeERC20Transfer(feeWallet, amount)
		if err != nil {
			return nil, nil, apperrors.Internal("failed to encode fee transfer")
		}
		unsignedTx, err = s.Blockchain.BuildContractCallTx(ctx, user.Address, token.ContractAddress, big.NewInt(0), data, nil)
		if err != nil {
			return nil, nil, apperrors.Internal("failed to build fee payment transaction: " + err.Error())
		}
	}

	now := time.Now()
	asset.Status = models.StatusApplicationConfirmed
	asset.DateSubmitted = &now
	if err := s.DB.Save(asset).Error; err != nil {
		return nil, nil, apperrors.Internal("failed to confirm application")
	}

	if signedTransaction != nil && *signedTransaction != "" {
		if _, err := s.Blockchain.SubmitSignedTransaction(ctx, *signedTransaction); err != nil {
			return nil, nil, apperrors.Internal("failed to submit fee payment: " + err.Error())
		}
	}

	return asset, unsignedTx, nil
}

// ConfirmFeePayment moves ApplicationConfirmed → FeeConfirmed. Requires
// the application to have already passed admin vetting (VettingStatus),
// mirroring upstream's exact ordering (vetting can complete at any point
// after Status 1; the initiator's fee-payment confirmation can only
// proceed once it has).
func (s *Service) ConfirmFeePayment(userID, assetID uint) (*models.TokenizedAsset, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.InitiatorUserID != userID {
		return nil, apperrors.Forbidden("you may only confirm your own application")
	}
	if asset.Status != models.StatusApplicationConfirmed {
		return nil, apperrors.Conflict("application is not awaiting fee confirmation")
	}
	if !asset.VettingStatus {
		return nil, apperrors.Conflict("application is still being vetted")
	}
	asset.Status = models.StatusFeeConfirmed
	if err := s.DB.Save(asset).Error; err != nil {
		return nil, apperrors.Internal("failed to confirm fee payment")
	}
	return asset, nil
}

// VetInput is the admin vetting payload - stakeholder selection plus each
// stakeholder's fee override (an empty override falls back to that
// stakeholder's own default fee, per stakeholderFeeOrDefault).
type VetInput struct {
	ApprovedAssetCustodianID                     uint
	CustodianFeePercent, CustodianFeeFixed       string
	AssetManagerID                               uint
	AssetManagerFeePercent, AssetManagerFeeFixed string
	AssetIssuingHouseID                          uint
	IssuingHouseFeePercent, IssuingHouseFeeFixed string
	LegalAndProfessionalPartnerID                uint
	LegalPartnerFeePercent, LegalPartnerFeeFixed string
	RatingAgencyID                               uint
	RatingAgencyFeePercent, RatingAgencyFeeFixed string
	TrusteeID                                    uint
	TrusteeFeePercent, TrusteeFeeFixed           string
	LegalAdviserID                               uint
	FinancialAdviserID                           uint
	ProceedPayoutCurrency                        string
	AssetQuoteCurrency                           string
	ExcludeSecFee                                bool
}

// statusRank orders Status values by pipeline position - needed because
// Status is a string enum, so Go's built-in > / < would compare
// alphabetically (e.g. "DRAFT" > "APPLICATION_CONFIRMED" is true
// lexicographically, which is not the intended ordering at all).
var statusRank = map[models.Status]int{
	models.StatusDraft:                0,
	models.StatusApplicationConfirmed: 1,
	models.StatusFeeConfirmed:         2,
	models.StatusFeeAcknowledged:      3,
	models.StatusMinted:               4,
	models.StatusPrimarySaleActive:    5,
	models.StatusSecondarySaleActive:  6,
}

func stringOrDefault(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// Vet is the admin/staff review step: it selects the stakeholders serving
// this asset, snapshots their fees onto the asset, and requires the
// primary-sale cap window to fit inside the sales window
// (SalesStart+CapDurationInDays <= SalesEnd, upstream's own check). It does
// not itself advance Status - only VettingStatus, exactly as upstream.
func (s *Service) Vet(assetID uint, input VetInput) (*models.TokenizedAsset, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if statusRank[asset.Status] > statusRank[models.StatusApplicationConfirmed] {
		return nil, apperrors.Conflict("application has passed the vetting stage")
	}

	var custodian models.ApprovedAssetCustodian
	if err := s.DB.First(&custodian, input.ApprovedAssetCustodianID).Error; err != nil {
		return nil, apperrors.BadRequest("unknown approved asset custodian")
	}
	var manager models.AssetManager
	if err := s.DB.First(&manager, input.AssetManagerID).Error; err != nil {
		return nil, apperrors.BadRequest("unknown asset manager")
	}
	var issuingHouse models.AssetIssuingHouse
	if err := s.DB.First(&issuingHouse, input.AssetIssuingHouseID).Error; err != nil {
		return nil, apperrors.BadRequest("unknown asset issuing house")
	}
	var legalPartner models.LegalAndProfessionalPartner
	if err := s.DB.First(&legalPartner, input.LegalAndProfessionalPartnerID).Error; err != nil {
		return nil, apperrors.BadRequest("unknown legal and professional partner")
	}
	var ratingAgency models.RatingAgency
	if err := s.DB.First(&ratingAgency, input.RatingAgencyID).Error; err != nil {
		return nil, apperrors.BadRequest("unknown rating agency")
	}
	var trustee models.Trustee
	if err := s.DB.First(&trustee, input.TrusteeID).Error; err != nil {
		return nil, apperrors.BadRequest("unknown trustee")
	}

	if asset.SalesStart != nil && asset.SalesEnd != nil {
		capEnd := asset.SalesStart.AddDate(0, 0, asset.CapDurationInDays)
		if capEnd.After(*asset.SalesEnd) {
			return nil, apperrors.BadRequest("cap end date exceeds the sales end date")
		}
	}

	asset.ApprovedAssetCustodianID = input.ApprovedAssetCustodianID
	asset.AssetManagerID = input.AssetManagerID
	asset.AssetIssuingHouseID = input.AssetIssuingHouseID
	asset.LegalAndProfessionalPartnerID = input.LegalAndProfessionalPartnerID
	asset.RatingAgencyID = input.RatingAgencyID
	asset.TrusteeID = input.TrusteeID
	asset.LegalAdviserID = input.LegalAdviserID
	asset.FinancialAdviserID = input.FinancialAdviserID
	asset.ExcludeSecFee = input.ExcludeSecFee
	if input.ProceedPayoutCurrency != "" {
		asset.ProceedPayoutCurrency = input.ProceedPayoutCurrency
	}
	if input.AssetQuoteCurrency != "" {
		asset.AssetQuoteCurrency = input.AssetQuoteCurrency
	}

	asset.CustodianFeePercent = stringOrDefault(input.CustodianFeePercent, custodian.FeePercent)
	asset.CustodianFeeFixed = stringOrDefault(input.CustodianFeeFixed, custodian.FeeFixed)
	asset.AssetManagerFeePercent = stringOrDefault(input.AssetManagerFeePercent, manager.FeePercent)
	asset.AssetManagerFeeFixed = stringOrDefault(input.AssetManagerFeeFixed, manager.FeeFixed)
	asset.IssuingHouseFeePercent = stringOrDefault(input.IssuingHouseFeePercent, issuingHouse.FeePercent)
	asset.IssuingHouseFeeFixed = stringOrDefault(input.IssuingHouseFeeFixed, issuingHouse.FeeFixed)
	asset.LegalPartnerFeePercent = stringOrDefault(input.LegalPartnerFeePercent, legalPartner.FeePercent)
	asset.LegalPartnerFeeFixed = stringOrDefault(input.LegalPartnerFeeFixed, legalPartner.FeeFixed)
	asset.RatingAgencyFeePercent = stringOrDefault(input.RatingAgencyFeePercent, ratingAgency.FeePercent)
	asset.RatingAgencyFeeFixed = stringOrDefault(input.RatingAgencyFeeFixed, ratingAgency.FeeFixed)
	asset.TrusteeFeePercent = stringOrDefault(input.TrusteeFeePercent, trustee.FeePercent)
	asset.TrusteeFeeFixed = stringOrDefault(input.TrusteeFeeFixed, trustee.FeeFixed)
	asset.VettingStatus = true

	if err := s.DB.Save(asset).Error; err != nil {
		return nil, apperrors.Internal("failed to save vetting")
	}
	return asset, nil
}

// FailDueDiligence resets an application back to Draft with a recorded
// reason - upstream requires at least 5 characters of explanation.
func (s *Service) FailDueDiligence(assetID uint, reason string) (*models.TokenizedAsset, error) {
	if len(reason) < 5 {
		return nil, apperrors.BadRequest("a due diligence failure reason of at least 5 characters is required")
	}
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	asset.Status = models.StatusDraft
	asset.VettingStatus = false
	asset.DueDiligenceFailed = true
	asset.DueDiligenceFailureReason = reason
	if err := s.DB.Save(asset).Error; err != nil {
		return nil, apperrors.Internal("failed to record due diligence failure")
	}
	return asset, nil
}

// AcknowledgeFeePayment moves FeeConfirmed → FeeAcknowledged: staff
// confirming the uploaded proof of payment is legitimate. The next step
// (minting) needs the asset's per-asset issuer/distribution addresses
// resolved, which are pure functions of the asset's own ID, so - unlike
// upstream's AssignIssuingWallet side effect here - there is nothing to
// assign yet; deriving them happens lazily when minting actually executes.
func (s *Service) AcknowledgeFeePayment(assetID uint) (*models.TokenizedAsset, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.Status != models.StatusFeeConfirmed {
		return nil, apperrors.Conflict("application is not awaiting fee acknowledgement")
	}
	now := time.Now()
	asset.Status = models.StatusFeeAcknowledged
	asset.DateOfApproval = &now
	if err := s.DB.Save(asset).Error; err != nil {
		return nil, apperrors.Internal("failed to acknowledge fee payment")
	}
	return asset, nil
}

// UpdateSalesDates lets staff adjust the primary-sale window before
// minting closes it in.
func (s *Service) UpdateSalesDates(assetID uint, salesStart, salesEnd time.Time) (*models.TokenizedAsset, error) {
	if !salesStart.Before(salesEnd) {
		return nil, apperrors.BadRequest("salesStart must be before salesEnd")
	}
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	asset.SalesStart = &salesStart
	asset.SalesEnd = &salesEnd
	if err := s.DB.Save(asset).Error; err != nil {
		return nil, apperrors.Internal("failed to update sales dates")
	}
	return asset, nil
}

// Delete removes a Draft application (and its documents) - upstream only
// allows this before the application "passes the editing stage".
func (s *Service) Delete(userID uint, assetID uint, requireOwnership bool) error {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return err
	}
	if requireOwnership && asset.InitiatorUserID != userID {
		return apperrors.Forbidden("you may only delete your own application")
	}
	if asset.Status != models.StatusDraft {
		return apperrors.Conflict("application has passed the editing stage and can no longer be deleted")
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tokenized_asset_id = ?", assetID).Delete(&models.AssetTokenizationDocument{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tokenized_asset_id = ?", assetID).Delete(&models.TokenizationFeeProofOfPayment{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.TokenizedAsset{}, assetID).Error
	})
}

// FeeWalletAddress is the single derived address application fees and
// mint-time asset fees are paid to - the equivalent of upstream's
// configured TOKENIZATION_APPLICATION_FEE wallet, but a
// cryptoutil.DeriveKey-derived address (no raw private key configured or
// stored) matching every other server-controlled role in this port.
func (s *Service) FeeWalletAddress() (string, error) {
	key, err := cryptoutil.DeriveKey(s.IssuerKeySalt + "|tokenization-fee-wallet")
	if err != nil {
		return "", apperrors.Internal("failed to derive the fee wallet address")
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), nil
}
