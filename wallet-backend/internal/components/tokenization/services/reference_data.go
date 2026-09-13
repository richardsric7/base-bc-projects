package services

import (
	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
)

// ReferenceData is the full dropdown/lookup surface the application form
// (and its public/admin variants) needs - direct port of upstream's
// equivalent bundle of getters, returned as one struct so a single
// endpoint call populates every dropdown at once, matching upstream's own
// getPublicTokenizationHandler/getTokenizationHandler/
// getTrovoManagerTokenizationHandler shape.
type ReferenceData struct {
	Sectors           []models.TokenizedAssetSector                  `json:"sectors"`
	SubSectors        []models.TokenizedAssetSubSector               `json:"subSectors"`
	Types             []models.TokenizedAssetType                    `json:"types"`
	DocumentTypes     []models.AssetTokenizationDocumentType         `json:"documentTypes"`
	Custodians        []models.ApprovedAssetCustodian                `json:"custodians"`
	Managers          []models.AssetManager                          `json:"managers"`
	IssuingHouses     []models.AssetIssuingHouse                     `json:"issuingHouses"`
	LegalPartners     []models.LegalAndProfessionalPartner           `json:"legalPartners"`
	RatingAgencies    []models.RatingAgency                          `json:"ratingAgencies"`
	Trustees          []models.Trustee                               `json:"trustees"`
	Currencies        []models.TokenizationCurrency                  `json:"currencies"`
	AllowedCountries  []models.TokenizationPublicAssetAllowedCountry `json:"allowedCountries"`
	Fees              []models.TokenizationFee                       `json:"fees,omitempty"`
	ProtectionOptions []models.AssetProtectionOption                 `json:"protectionOptions,omitempty"`
	ProceedCycles     []models.ProceedCycle                          `json:"proceedCycles,omitempty"`
	PaymentMethods    []models.TokenizationFeePaymentMethod          `json:"paymentMethods,omitempty"`
	CountryConfigs    []models.TokenizationCountryConfig             `json:"countryConfigs,omitempty"`
}

// PublicReferenceData returns the subset shown on the unauthenticated
// public discovery surface (no fees/protection-options/proceed-
// cycles/payment-methods/country-configs - those only matter once you're
// actually applying).
func (s *Service) PublicReferenceData() (*ReferenceData, error) {
	data, err := s.referenceData()
	if err != nil {
		return nil, err
	}
	data.Fees = nil
	data.ProtectionOptions = nil
	data.ProceedCycles = nil
	data.PaymentMethods = nil
	data.CountryConfigs = nil
	return data, nil
}

// FullReferenceData returns everything - the authed-user and admin variant.
func (s *Service) FullReferenceData() (*ReferenceData, error) {
	return s.referenceData()
}

func (s *Service) referenceData() (*ReferenceData, error) {
	var data ReferenceData
	loaders := []func() error{
		func() error { return s.DB.Find(&data.Sectors).Error },
		func() error { return s.DB.Find(&data.SubSectors).Error },
		func() error { return s.DB.Find(&data.Types).Error },
		func() error { return s.DB.Find(&data.DocumentTypes).Error },
		func() error { return s.DB.Find(&data.Custodians).Error },
		func() error { return s.DB.Find(&data.Managers).Error },
		func() error { return s.DB.Find(&data.IssuingHouses).Error },
		func() error { return s.DB.Find(&data.LegalPartners).Error },
		func() error { return s.DB.Find(&data.RatingAgencies).Error },
		func() error { return s.DB.Find(&data.Trustees).Error },
		func() error { return s.DB.Find(&data.Currencies).Error },
		func() error { return s.DB.Find(&data.AllowedCountries).Error },
		func() error { return s.DB.Where("inactive = ?", false).Find(&data.Fees).Error },
		func() error { return s.DB.Find(&data.ProtectionOptions).Error },
		func() error { return s.DB.Find(&data.ProceedCycles).Error },
		func() error { return s.DB.Where("inactive = ?", false).Find(&data.PaymentMethods).Error },
		func() error { return s.DB.Find(&data.CountryConfigs).Error },
	}
	for _, load := range loaders {
		if err := load(); err != nil {
			return nil, apperrors.Internal("failed to load reference data")
		}
	}
	return &data, nil
}

// TypesBySubSector narrows the type dropdown once a sub-sector is chosen.
func (s *Service) TypesBySubSector(subSectorID string) ([]models.TokenizedAssetType, error) {
	var types []models.TokenizedAssetType
	if err := s.DB.Where("tokenized_asset_sub_sector_id = ?", subSectorID).Find(&types).Error; err != nil {
		return nil, apperrors.Internal("failed to load types")
	}
	return types, nil
}

// IsMintingApprover and IsMintingInitiator check the global staff
// allow-lists - independent of a specific asset's own MintingApprovers/
// MintingInitiators CSV (see minting.go).
func (s *Service) IsMintingApprover(userID uint) bool {
	var count int64
	s.DB.Model(&models.TokenizationMintingApprover{}).Where("user_id = ?", userID).Count(&count)
	return count > 0
}

func (s *Service) IsMintingInitiator(userID uint) bool {
	var count int64
	s.DB.Model(&models.TokenizationMintingInitiator{}).Where("user_id = ?", userID).Count(&count)
	return count > 0
}
