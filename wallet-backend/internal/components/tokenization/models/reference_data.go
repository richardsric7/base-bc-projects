package models

// This file holds tokenization's reference-data surface: sectors,
// stakeholder directories (custodians/managers/issuing houses/legal
// partners/rating agencies/trustees), fees, currencies, protection
// options, proceed cycles, allowed countries, per-country compliance
// config, and the minting staff allow-lists. All of it is chain-agnostic
// and ports as a direct schema translation - see PLAN.md §4.9.

// TokenizedAssetSector and TokenizedAssetSubSector/TokenizedAssetType form
// the sector → sub-sector → type hierarchy the application form's
// dropdowns are populated from.
type TokenizedAssetSector struct {
	ID                  string `gorm:"primaryKey;size:100" json:"id"`
	RequirementDocument string `json:"requirementDocument"`
}

type TokenizedAssetSubSector struct {
	ID                     string `gorm:"primaryKey;size:100" json:"id"`
	TokenizedAssetSectorID string `gorm:"size:100;index" json:"assetSectorId"`
}

type TokenizedAssetType struct {
	ID                        uint   `gorm:"primaryKey" json:"id"`
	TokenizedAssetSubSectorID string `gorm:"size:100;uniqueIndex:idx_asset_type_unique" json:"assetSubSectorId"`
	AssetType                 string `gorm:"size:100;uniqueIndex:idx_asset_type_unique" json:"assetType"`
}

// Each stakeholder directory shares the same shape: a name/address/country
// plus the fee this stakeholder charges by default (overridable per-asset
// at vetting time - see TokenizedAsset's fee-snapshot fields).
type ApprovedAssetCustodian struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:255;not null" json:"name"`
	Address     string `json:"address"`
	CountryCode string `gorm:"size:3" json:"countryCode"`
	FeePercent  string `json:"feePercent"`
	FeeFixed    string `json:"feeFixed"`
}

type AssetManager struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:255;not null" json:"name"`
	Address     string `json:"address"`
	CountryCode string `gorm:"size:3" json:"countryCode"`
	FeePercent  string `json:"feePercent"`
	FeeFixed    string `json:"feeFixed"`
}

type AssetIssuingHouse struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:255;not null" json:"name"`
	Address     string `json:"address"`
	CountryCode string `gorm:"size:3" json:"countryCode"`
	FeePercent  string `json:"feePercent"`
	FeeFixed    string `json:"feeFixed"`
}

type LegalAndProfessionalPartner struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:255;not null" json:"name"`
	Address     string `json:"address"`
	CountryCode string `gorm:"size:3" json:"countryCode"`
	FeePercent  string `json:"feePercent"`
	FeeFixed    string `json:"feeFixed"`
}

type RatingAgency struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:255;not null" json:"name"`
	Address     string `json:"address"`
	CountryCode string `gorm:"size:3" json:"countryCode"`
	FeePercent  string `json:"feePercent"`
	FeeFixed    string `json:"feeFixed"`
}

type Trustee struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"size:255;not null" json:"name"`
	Address     string `json:"address"`
	CountryCode string `gorm:"size:3" json:"countryCode"`
	FeePercent  string `json:"feePercent"`
	FeeFixed    string `json:"feeFixed"`
}

// TokenizationFee is a selectable application-fee tier.
type TokenizationFee struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	FeeFiatPercentage  string `json:"feeFiatPercentage"`
	FeeFiatCap         string `json:"feeFiatCap"` // minimum fee
	FeeAssetPercentage string `json:"feeAssetPercentage"`
	FeeDescription     string `json:"feeDescription"`
	CountryCode        string `gorm:"size:2;default:NG" json:"countryCode"`
	Inactive           bool   `gorm:"default:false" json:"-"`
}

// TokenizationCurrency is the allow-list of settlement/payout currencies -
// a thin wrapper around a CuratedToken symbol rather than upstream's own
// code+issuer pair, since CuratedToken already carries the contract
// address/decimals (see PLAN.md §4.9's "Quote currency" note).
type TokenizationCurrency struct {
	Symbol string `gorm:"primaryKey;size:12" json:"symbol"`
	Label  string `gorm:"size:32" json:"label"`
}

// AssetProtectionOption and ProceedCycle are flat reference lists.
type AssetProtectionOption struct {
	ID string `gorm:"primaryKey;size:100" json:"id"`
}

type ProceedCycle struct {
	ID string `gorm:"primaryKey;size:50" json:"id"`
}

// TokenizationPublicAssetAllowedCountry gates PUBLIC offerings by country.
type TokenizationPublicAssetAllowedCountry struct {
	CountryCode string `gorm:"primaryKey;size:3" json:"countryCode"`
}

// TokenizationCountryConfig holds the fee/compliance settings that are
// genuinely per-country. Upstream's InternalBalanceTokenCode/
// InternalTokenIssuer pair (a Stellar asset code+issuer identifying the
// synthetic intermediate quote-currency asset Stellar's path-payment
// engine routed a purchase through) is not ported as-is - PLAN.md §4.9's
// "Quote currency" note explains why the multi-hop path-payment routing
// itself has no Base equivalent worth building (direct ERC-20 transfers
// already do the job in one hop). What IS restored, per PLAN.md §22.4:
// the restricted-holding requirement upstream enforced on that asset
// (`checkDistributionWalletHasQuoteCurrencyAuthorization`) - see
// InternalBalanceContractAddress below.
type TokenizationCountryConfig struct {
	CountryCode                              string `gorm:"primaryKey;size:2" json:"countryCode"`
	SECTokenizationFeePercent                string `json:"secTokenizationFeePercent"`
	SECTokenizationFeeFixed                  string `json:"secTokenizationFeeFixed"`
	SECTradeFeePercent                       string `json:"secTradeFeePercent"`
	SECTradeFeeFixed                         string `json:"secTradeFeeFixed"`
	RegulatorName                            string `gorm:"default:'SECURITIES AND EXCHANGE COMMISSION'" json:"regulatorName"`
	MinTokenizationFee                       string `json:"minTokenizationFee"`
	MinTROVBalanceForTokenizationApplication string `json:"minTrovBalanceForTokenizationApplication"`
	TokenizationApplicationFee               string `json:"tokenizationApplicationFee"`
	TokenizationApplicationFeeAsset          string `gorm:"default:TROV" json:"tokenizationApplicationFeeAsset"` // a CuratedToken symbol
	VATPercent                               string `json:"vatPercent"`
	// InternalBalanceContractAddress is this country's deployed internal-
	// balance restricted asset - the same TokenizedAsset.sol contract
	// tokenized assets themselves use (PLAN.md §22.1's isAuthorized
	// allow-list), replacing upstream's InternalBalanceTokenCode/
	// InternalTokenIssuer pair. Nil until DeployInternalBalanceAsset is
	// called for this country (services/internal_balance.go); a country
	// with no deployed internal-balance asset simply carries no
	// restriction on it yet, the same posture an un-minted TokenizedAsset
	// has toward its own authorization gate.
	InternalBalanceContractAddress *string `json:"internalBalanceContractAddress,omitempty"`
}

// TokenizationMintingApprover and TokenizationMintingInitiator are the
// global staff allow-lists gating who may call the mint endpoint at all -
// independent of the per-asset MintingApprovers/MintingInitiators CSV
// (TokenizedAsset) that determines whose signoff a specific asset's mint
// needs. See PLAN.md §4.9 and minting.go.
type TokenizationMintingApprover struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	UserID uint `gorm:"uniqueIndex;not null" json:"userId"`
}

type TokenizationMintingInitiator struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	UserID uint `gorm:"uniqueIndex;not null" json:"userId"`
}
