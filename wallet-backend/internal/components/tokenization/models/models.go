// Package models defines the persisted shapes for tokenization - the
// original's largest subsystem (representing and trading a tokenized
// real-world asset). See PLAN.md §4.9 for the full design: what ports
// directly (the bulk of these fields are chain-agnostic business/
// compliance data), what's substituted (an issuer/distribution/sale
// contract instead of a Stellar issuer account and standing DEX offer),
// and what's simplified or dropped outright (no trustline/opt-in concept,
// no synthetic intermediate "internal balance" quote currency, no
// path-payment pathfinding).
package models

import "time"

// Status is a TokenizedAsset's lifecycle state - the same seven states the
// original tracked as a bare int, named here for clarity. Every transition
// and its trigger is documented in PLAN.md §4.9.
type Status string

const (
	StatusDraft                Status = "DRAFT"
	StatusApplicationConfirmed Status = "APPLICATION_CONFIRMED"
	StatusFeeConfirmed         Status = "FEE_CONFIRMED"
	StatusFeeAcknowledged      Status = "FEE_ACKNOWLEDGED"
	StatusMinted               Status = "MINTED"
	StatusPrimarySaleActive    Status = "PRIMARY_SALE_ACTIVE"
	StatusSecondarySaleActive  Status = "SECONDARY_SALE_ACTIVE"
)

// OfferingType gates who may see/subscribe to an asset - PUBLIC needs a
// SEC approval number, PRIVATE needs a ClosedGroupID (sharedaccess's
// ClosedGroup with Purpose PRIVATE_OFFERING, per PLAN.md §4.2/§4.9).
type OfferingType string

const (
	OfferingPrivate OfferingType = "PRIVATE"
	OfferingPublic  OfferingType = "PUBLIC"
)

// ProceedPayoutType is how dividend/proceed payouts (the dormant engine in
// payout.go) would be denominated, ported from upstream as a field even
// though nothing currently reads it - see PLAN.md §4.9's payout-engine note.
type ProceedPayoutType string

const (
	ProceedPayoutCrypto ProceedPayoutType = "CRYPTO"
	ProceedPayoutFiat   ProceedPayoutType = "FIAT"
)

// TokenizedAsset is one asset's full application-through-trading record.
// Monetary/quantity fields are decimal strings (never float64), matching
// this codebase's convention everywhere else (see market.MarketOffer) -
// parsed with shopspring/decimal in the services layer.
type TokenizedAsset struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	DateSubmitted  *time.Time `json:"dateSubmitted"`
	DateOfApproval *time.Time `json:"dateOfApproval"`
	MintingDate    *time.Time `json:"mintingDate"`

	// InitiatorUserID is the applicant - a users.User.ID, replacing the
	// original's raw InitiatorUsername with this codebase's usual FK
	// convention (see fiat/crypto/kyc).
	InitiatorUserID uint `gorm:"index;not null" json:"initiatorUserId"`

	AssetSector          string `gorm:"size:128" json:"assetSector"`
	AssetSubSector       string `gorm:"size:128" json:"assetSubSector"`
	AssetType            string `gorm:"size:128" json:"assetType"`
	AssetName            string `gorm:"size:255" json:"assetName"`
	AssetCode            string `gorm:"size:12;uniqueIndex" json:"assetCode"`
	AssetLogo            string `json:"assetLogo"`
	AssetWebsite         string `gorm:"default:trovotech.io" json:"assetWebsite"`
	AssetDescription     string `gorm:"type:text" json:"assetDescription"`
	AssetCountryLocation string `gorm:"size:2;not null;default:NG" json:"assetCountryLocation"`
	AssetPhysicalAddress string `json:"assetPhysicalAddress"`
	AssetLongitude       string `json:"assetLongitude"`
	AssetLatitude        string `json:"assetLatitude"`

	OwnershipType                string `json:"ownershipType"` // DIRECT, THIRD_PARTY
	OwnershipKind                string `json:"ownershipKind"` // INDIVIDUAL, CORPORATE
	InitialOwnerPreferredAddress string `gorm:"size:42" json:"initialOwnerPreferredAddress"`
	AssetOwnerName               string `json:"assetOwnerName"`
	AssetOwnerAddress            string `json:"assetOwnerAddress"`

	ApprovedAssetCustodianID      uint `json:"approvedAssetCustodianId"`
	AssetManagerID                uint `json:"assetManagerId"`
	AssetIssuingHouseID           uint `json:"assetIssuingHouseId"`
	LegalAndProfessionalPartnerID uint `json:"legalAndProfessionalPartnerId"`
	RatingAgencyID                uint `json:"ratingAgencyId"`
	TrusteeID                     uint `json:"trusteeId"`
	LegalAdviserID                uint `json:"legalAdviserId"`
	FinancialAdviserID            uint `json:"financialAdviserId"`

	OfferingType      OfferingType `gorm:"size:16;default:PRIVATE" json:"offeringType"`
	ClosedGroupID     *uint        `json:"closedGroupId"` // sharedaccess.ClosedGroup.ID, Purpose=PRIVATE_OFFERING
	SecApprovalNumber string       `json:"secApprovalNumber"`

	// IssuerContractAddress is the deployed TokenizedAsset.sol address -
	// Base's issuer is a contract, not an account (PLAN.md §4.9).
	IssuerContractAddress *string `gorm:"size:42" json:"issuerContractAddress"`
	// SaleContractAddress is the deployed Sale.sol address - the direct
	// equivalent of the original's standing ManageSellOffer.
	SaleContractAddress *string `gorm:"size:42" json:"saleContractAddress"`
	// DistributionAddress is the per-asset cryptoutil.DeriveKey-derived
	// address holding un-sold supply and signing fiat-purchase transfers -
	// the equivalent of the original's MarketMakingWallet/
	// WalletToHoldAssetsNotForSale.
	DistributionAddress *string `gorm:"size:42" json:"distributionAddress"`

	Status                    Status `gorm:"size:24;not null;default:DRAFT" json:"status"`
	VettingStatus             bool   `gorm:"default:false" json:"vettingStatus"`
	DueDiligenceFailed        bool   `gorm:"default:false" json:"dueDiligenceFailed"`
	DueDiligenceFailureReason string `json:"dueDiligenceFailureReason"`

	AssetCurrentValue                    string `json:"assetCurrentValue"`
	AssetOwnerRetainedOrContributedValue string `json:"assetOwnerRetainedOrContributedValue"`
	ValueOfTokenizedAsset                string `json:"valueOfTokenizedAsset"`
	InitialValueOfTokenizedAsset         string `json:"initialValueOfTokenizedAsset"`

	NumberOfTokenToBeIssued          string `json:"numberOfTokenToBeIssued"`
	MaxNumberOfTokenAvailableForSale string `json:"maxNumberOfTokenAvailableForSale"`
	NumberOfTokenToBeSold            string `json:"numberOfTokenToBeSold"`
	TotalTokenHeldByManager          string `json:"totalTokenHeldByManager"`
	PricePerToken                    string `json:"pricePerToken"`
	AssetDecimals                    uint8  `gorm:"default:2" json:"assetDecimals"`

	// AssetQuoteCurrency is a CuratedToken symbol (e.g. "USDC") - see
	// PLAN.md §4.9's "Quote currency" note for why this needs no separate
	// issuer lookup or intermediate currency the way upstream's
	// CountryConfig-resolved internal-balance-token scheme did.
	AssetQuoteCurrency string `gorm:"default:USDC" json:"assetQuoteCurrency"`

	SalesStart        *time.Time `json:"salesStart"`
	SalesEnd          *time.Time `json:"salesEnd"`
	CapOnPurchase     bool       `json:"capOnPurchase"`
	CapQuantity       string     `json:"capQuantity"`
	CapAmountInFiat   string     `json:"capAmountInFiat"`
	CapDurationInDays int        `json:"capDurationInDays"`
	ProceedCycle      string     `json:"proceedCycle"`

	// EarlyExitPenaltyPercent/EarlyExitFeePercent/CurrentNAVPerToken/
	// MaturityDate apply to every asset class (upstream stores these as
	// plain columns on its one flat table despite the fields being
	// declared amid its REIT/yield-fund/bond sections - the early-exit
	// code path reads them unconditionally regardless of asset type, so
	// they're core fields here rather than asset-class-specific ones).
	EarlyExitPenaltyPercent string     `json:"earlyExitPenaltyPercent"`
	EarlyExitFeePercent     string     `json:"earlyExitFeePercent"`
	CurrentNAVPerToken      string     `json:"currentNavPerToken"`
	MaturityDate            *time.Time `json:"maturityDate"`

	TokenizationFeeID *uint `json:"tokenizationFeeId"`
	ExcludeSecFee     bool  `json:"excludeSecFee"`

	// Stakeholder fee snapshot, populated at vetting time - each
	// percent/fixed/computed-value triple mirrors upstream field-for-field.
	SECFeePercent, SECFeeFixed, SECFeeValue                            string
	CustodianFeePercent, CustodianFeeFixed, CustodianFeeValue          string
	AssetManagerFeePercent, AssetManagerFeeFixed, AssetManagerFeeValue string
	IssuingHouseFeePercent, IssuingHouseFeeFixed, IssuingHouseFeeValue string
	LegalPartnerFeePercent, LegalPartnerFeeFixed, LegalPartnerFeeValue string
	RatingAgencyFeePercent, RatingAgencyFeeFixed, RatingAgencyFeeValue string
	TrusteeFeePercent, TrusteeFeeFixed, TrusteeFeeValue                string
	VATPercent, VATValue, VATInAsset                                   string

	FeeInAsset        string `json:"feeInAsset"`
	FeeInAssetPercent string `json:"feeInAssetPercent"`
	FeeInFiat         string `json:"feeInFiat"`

	// ProceedPayoutCurrency is a CuratedToken symbol; ProceedPayoutType
	// distinguishes a crypto vs. fiat payout preference for the dormant
	// payout engine (payout.go) - ported, never executed, per PLAN.md §4.9.
	ProceedPayoutCurrency string            `json:"proceedPayoutCurrency"`
	ProceedPayoutType     ProceedPayoutType `gorm:"size:8;default:CRYPTO" json:"proceedPayoutType"`

	ExemptedCountries             string `json:"exemptedCountries"` // CSV of 2-letter codes
	HasAdditionalKYCRequirements  bool   `json:"hasAdditionalKycRequirements"`
	AdditionalKYCRequirements     string `json:"additionalKycRequirements"`
	InvestorAccreditationRequired bool   `json:"investorAccreditationRequired"`

	// MintingInitiators/MintingApprovers are CSVs of Base addresses - the
	// per-asset multisig-signer list gating MintRegulatedTokenizedAsset,
	// distinct from the global TokenizationMintingApprover/Initiator
	// allow-list (reference_data.go, keyed by UserID) that gates who may
	// call it at all. Addresses rather than usernames (upstream's own
	// shape) since a signature is verified against an address directly -
	// no extra username-to-address resolution step needed, consistent
	// with sharedaccess's GroupMember using MemberAddress for the same
	// kind of per-action signer list. See PLAN.md §4.9 and minting.go's
	// MintApproval for how the ≥4-signoff threshold is enforced.
	MintingInitiators string `json:"mintingInitiators"`
	MintingApprovers  string `json:"mintingApprovers"`

	BankID          *uint  `json:"bankId"`
	AccountNumber   string `json:"accountNumber"`
	BeneficiaryName string `json:"beneficiaryName"`

	TokenizationApplicationFee      string `json:"tokenizationApplicationFee"`
	TokenizationApplicationFeeAsset string `json:"tokenizationApplicationFeeAsset"` // a CuratedToken symbol

	ProtectionMethods          string `json:"protectionMethods"` // CSV
	InsuranceCompanyName       string `json:"insuranceCompanyName"`
	InsurancePolicyNumber      string `json:"insurancePolicyNumber"`
	InsurancePolicyHolder      string `json:"insurancePolicyHolder"`
	PercentageValueOfInsurance string `json:"percentageValueOfInsurance"`

	IsFreeFromLiensAndEncumbrances bool `json:"isFreeFromLiensAndEncumbrances"`
	AssetAlreadyExists             bool `json:"assetAlreadyExists"`

	AgreeTransferTitleToCustodian               bool   `json:"agreeTransferTitleToCustodian"`
	ContractualProtectionRevGuarantees          bool   `json:"contractualProtectionRevGuarantees"`
	ContractualProtectionPerfBond               bool   `json:"contractualProtectionPerfBond"`
	ContractualProtectionSLA                    bool   `json:"contractualProtectionSla"`
	RiskSharingMechanismPPPs                    bool   `json:"riskSharingMechanismPpPs"`
	RiskSharingMechanismHedgeInstruments        bool   `json:"riskSharingMechanismHedgeInstruments"`
	RiskSharingMechanismCompletionGuarantees    bool   `json:"riskSharingMechanismCompletionGuarantees"`
	IndependentMonitoringList                   string `json:"independentMonitoringList"`
	ESGSafeguardsSusCerts                       bool   `json:"esgSafeguardsSusCerts"`
	ESGSafeguardsCommEngPlans                   bool   `json:"esgSafeguardsCommEngPlans"`
	SecurityMeasuresAccessControl               bool   `json:"securityMeasuresAccessControl"`
	SecurityMeasuresSurveillanceSystems         bool   `json:"securityMeasuresSurveillanceSystems"`
	SecurityMeasuresOnSiteSecurityPersonnel     bool   `json:"securityMeasuresOnSiteSecurityPersonnel"`
	SecurityMeasuresPerimeterSecurity           bool   `json:"securityMeasuresPerimeterSecurity"`
	SecurityMeasuresCriticalInfraProtections    bool   `json:"securityMeasuresCriticalInfraProtections"`
	OtherAssetProtection                        string `json:"otherAssetProtection"`
	LegalAdvisor                                string `json:"legalAdvisor"`
	FinancialAdvisor                            string `json:"financialAdvisor"`
	UndertakingNoLien                           bool   `json:"undertakingNoLien"`
	UndertakingNotCollateral                    bool   `json:"undertakingNotCollateral"`
	UndertakingNoClaims                         bool   `json:"undertakingNoClaims"`
	UndertakingNoForeclosure                    bool   `json:"undertakingNoForeclosure"`
	ComplianceNoViolation                       bool   `json:"complianceNoViolation"`
	ComplianceAllPermits                        bool   `json:"complianceAllPermits"`
	OutstandingFinancialRespNoDebts             bool   `json:"outstandingFinancialRespNoDebts"`
	OutstandingFinancialRespNoHiddenLiabilities bool   `json:"outstandingFinancialRespNoHiddenLiabilities"`
	RiskManagementFullyInsured                  bool   `json:"riskManagementFullyInsured"`
	RiskManagementDeclaredValue                 bool   `json:"riskManagementDeclaredValue"`
	PhysicalConditionSound                      bool   `json:"physicalConditionSound"`
	PhysicalConditionNoLease                    bool   `json:"physicalConditionNoLease"`
	PhysicalConditionNoUndisclosedEasements     bool   `json:"physicalConditionNoUndisclosedEasements"`

	ProjectStrategicObjectives                   string `json:"projectStrategicObjectives"`
	ProjectDevelopmentTimeline                   string `json:"projectDevelopmentTimeline"`
	ProjectKeyMilestoneAndDates                  string `json:"projectKeyMilestoneAndDates"`
	ProjectScope                                 string `json:"projectScope"`
	ProjectEconomicBenefits                      string `json:"projectEconomicBenefits"`
	ProjectExpectedNoOfJobs                      int    `json:"projectExpectedNoOfJobs"`
	ProjectIntendedSocialBenefits                string `json:"projectIntendedSocialBenefits"`
	ProjectTechnicalPartners                     string `json:"projectTechnicalPartners"`
	ProjectFinancialPartners                     string `json:"projectFinancialPartners"`
	EstimatedProjectIRR                          string `json:"estimatedProjectIrr"`
	EstimatedProjectROI                          string `json:"estimatedProjectRoi"`
	EstimatedProjectNPV                          string `json:"estimatedProjectNpv"`
	EstimatedProjectPaybackPeriodsInMonths       int    `json:"estimatedProjectPaybackPeriodsInMonths"`
	KeyAssumptionsList                           string `json:"keyAssumptionsList"`
	ProjectIdentifiedLegalRisks                  string `json:"projectIdentifiedLegalRisks"`
	ProjectIdentifiedRegulatoryRisks             string `json:"projectIdentifiedRegulatoryRisks"`
	ProjectIdentifiedOperationalOrExecutionRisks string `json:"projectIdentifiedOperationalOrExecutionRisks"`
	ProjectIdentifiedMarketRisks                 string `json:"projectIdentifiedMarketRisks"`
	ProjectIdentifiedOtherRelevantRisks          string `json:"projectIdentifiedOtherRelevantRisks"`
	DeepLink                                     string `json:"deepLink"`

	LastUpdatedBy string `json:"lastUpdatedBy"`

	// Asset-class-specific descriptive tail - see asset_class_fields.go.
	// Every field here is purely informational (display/compliance
	// narrative), never read by this component's own business logic, and
	// ports as a direct schema translation with nothing dropped.
	Bond            BondFields            `gorm:"embedded;embeddedPrefix:bond_" json:"bond"`
	Fund            FundFields            `gorm:"embedded;embeddedPrefix:fund_" json:"fund"`
	CommercialPaper CommercialPaperFields `gorm:"embedded;embeddedPrefix:cp_" json:"commercialPaper"`
	Commodity       CommodityFields       `gorm:"embedded;embeddedPrefix:commodity_" json:"commodity"`
	Vault           VaultFields           `gorm:"embedded;embeddedPrefix:vault_" json:"vault"`
	IssuerDebt      IssuerDebtFields      `gorm:"embedded;embeddedPrefix:debt_" json:"issuerDebt"`
	REIT            REITFields            `gorm:"embedded;embeddedPrefix:reit_" json:"reit"`
	PrivateEquity   PrivateEquityFields   `gorm:"embedded;embeddedPrefix:pe_" json:"privateEquity"`

	AssetTokenizationDocuments []AssetTokenizationDocument     `gorm:"foreignKey:TokenizedAssetID" json:"assetTokenizationDocuments,omitempty"`
	ProofOfPaymentDocuments    []TokenizationFeeProofOfPayment `gorm:"foreignKey:TokenizedAssetID" json:"proofOfPaymentDocuments,omitempty"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&TokenizedAsset{},
	&AssetTokenizationDocument{},
	&TokenizationFeeProofOfPayment{},
	&TokenizationFeePaymentMethod{},
	&TokenizedAssetSector{},
	&TokenizedAssetSubSector{},
	&TokenizedAssetType{},
	&AssetTokenizationDocumentType{},
	&ApprovedAssetCustodian{},
	&AssetManager{},
	&AssetIssuingHouse{},
	&LegalAndProfessionalPartner{},
	&RatingAgency{},
	&Trustee{},
	&TokenizationFee{},
	&TokenizationCurrency{},
	&AssetProtectionOption{},
	&ProceedCycle{},
	&TokenizationPublicAssetAllowedCountry{},
	&TokenizationCountryConfig{},
	&TokenizationMintingApprover{},
	&TokenizationMintingInitiator{},
	&MintApproval{},
	&MintApprovalSignoff{},
	&TokenizedAssetSubscription{},
	&ExpressionOfInterest{},
	&TokenizedAssetEarlyExit{},
	&ProceedPayout{},
	&TokenizedAssetPayoutSchedule{},
	&TokenizedAssetPayoutEngineTask{},
}
