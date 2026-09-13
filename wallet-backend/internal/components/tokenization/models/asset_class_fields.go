package models

import "time"

// This file holds the asset-class-specific descriptive tail: business/
// compliance narrative fields with no chain-specific content at all,
// present because one TokenizedAsset table serves every asset class
// (bond/note, mutual fund, REIT, commodity/warehouse receipt, vault-stored
// asset, private-equity/hybrid fund, commercial paper). Grouped into
// embedded structs (one per asset class) rather than upstream's single
// flat 550-column table, purely for readability - GORM's embeddedPrefix
// keeps each group's columns collision-free in the one underlying table.
// None of this is read by this component's own service logic; it exists
// only to be captured at application time and displayed back, exactly as
// upstream. Money/quantity/rate fields are decimal strings, per this
// codebase's convention.

// BondFields covers bond/note/mutual-fund-style debt instruments.
type BondFields struct {
	IsinOrSerialNumber                    string
	InstrumentName                        string
	InstrumentType                        string
	TotalIssueSize                        string
	IssueDate                             *time.Time
	FaceValuePerUnit                      string
	CouponOrInterestRateType              string
	CouponOrInterestRate                  string
	ReferenceIndex                        string
	ExpectedYield                         string
	EarlyRedemptionOptionInvestor         string
	MinimumInvestmentAmount               string
	TaxTreatmentTokenHolders              string
	PaymentStructureToTokenHolders        string
	RedemptionMethod                      string
	PaymentStructure                      string
	RepaymentMethod                       string
	PaymentCycle                          string
	WeightedAverageLife                   string
	UnderlyingAssetPoolSize               string
	PoolComposition                       string
	CreditEnhancementMethod               string
	SummaryOfUseOfProceeds                string
	CreditRatingIfAny                     string
	IssuerName                            string
	IssuerType                            string
	IssuerContactPerson                   string
	ContactEmail                          string
	ContactPhoneNumber                    string
	BriefCompanyOverview                  string
	MortgageOriginators                   string
	Servicer                              string
	InstrumentTrustee                     string
	InstrumentCustodians                  string
	InstrumentLegalAdvisor                string
	SpecialPurposeVehicle                 string
	InstrumentAssetManagerOrAdministrator string
	UnderwriterIfAny                      string
	InstrumentCreditRatingAgency          string
	AuditorOrVerifier                     string
	CreditRiskAssessment                  string
	CreditRating                          string
	PrepaymentRisk                        string
	InterestRateRisk                      string
	StructuralComplexityRisk              string
	LegalOrRegulatoryRisk                 string
	OperationalRisk                       string
	MarketRisk                            string
	EsgRisk                               string
	MitigationMeasures                    string
	IssuingAuthority                      string
	RegulatoryApprovalID                  string
	LicenseApprovalReferenceNumber        string
	ListingStatus                         string
	CurrencyOfIssuance                    string
	CouponRateType                        string
	CouponRate                            string
	SpreadOrMargin                        string
	ResetFrequency                        string
	CouponPaymentFrequency                string
	RedemptionStructure                   string
	EarlyRedemptionOption                 string
	EarlyRedemptionPenalty                string
	TaxTreatment                          string
	NavOrMarketValueUpdates               string
	ImpactMetrics                         string
	LegalBacking                          string
	DefaultHistory                        string
	RiskFactorsSummary                    string
	PayingAgent                           string
	Auditor                               string
	RegistrarOrCSCSAgent                  string
}

// FundFields covers mutual-fund/portfolio-style pooled investment vehicles.
type FundFields struct {
	FundStructure              string
	AssetManagementCompanyName string
	FundManagers               string
	RegulatoryLicenseNumber    string
	FundLaunchDate             *time.Time
	TotalExpenseRatio          string
	ExitLoadRedemptionFee      string
	TenureMonths               int
	InitialNetAssetValue       string
	NavUpdateFrequency         string
	NavCalculationMethod       string
	RedemptionRules            string
	LockInPeriodDays           int
	EntryLoad                  string
	PerformanceFee             string
	DividendPolicy             string
	LiquidityProfile           string
	DistributionFrequency      string
	DistributionMethod         string
	BenchmarkComparisonMethod  string
	FeeBreakdownSummary        string
	InvestmentObjective        string
	EquityStrategy             string
	MarketCapitalizationFocus  string
	BenchmarkIndex             string
	SectorExposureLimits       string
	TopHoldings                string
	GeographicExposure         string
	RiskProfile                string
	VolatilityEstimate         string
	DividendYield              string
	TrusteeName                string
	FundAdministrator          string
	InvestmentCommitteeMembers string
	IsinOrSecFundCode          string
	FundRiskRating             string
	AssetAllocation            string
	AverageMaturity            string
	YieldToMaturity            string
	CreditRatingProfile        string
	PerformanceFeeIfAny        string
	TopEquityHoldings          string
	TargetAllocation           string
	AllowedAllocationRange     string
	AssetClassesIncluded       string
	RebalancingFrequency       string
	BenchmarkIndexComposite    string
	TopDebtHoldings            string
	CreditRatingDistribution   string
}

// CommercialPaperFields covers short-term issuer debt.
type CommercialPaperFields struct {
	TitleOfIssuance          string
	TypeOfCommercialPaper    string
	PricingYield             string
	UseOfProceeds            string
	Ranking                  string
	BackingSecurity          string
	IssuerRegistrationNumber string
	IncorporationDate        *time.Time
	RcNumber                 string
	TaxIDNumber              string
	OfficeAddress            string
	Rating                   string
	PartyIssuer              string
	PartyArranger            string
	PartyLegalAdviser        string
	PartyAuditor             string
	PartyRatingAgency        string
	PartyCustodian           string
	PartyTrustee             string
	PartyAuditorVerifier     string
	SecurityLegalBacking     string
	SecurityCollateral       string
	SecurityDefaultHistory   string
	SecurityCreditRating     string
	RiskFactorsSummary       string
	BusinessRisk             string
	DefaultRisk              string
	LiquidityRisk            string
	RegulatoryRisk           string
	MarketRisk               string
	OperationalRisk          string
	MitigationMeasures       string
}

// CommodityFields covers commodity/warehouse-receipt-backed assets.
type CommodityFields struct {
	CommodityType                string
	CommodityDescription         string
	Quantity                     int
	QualityGrade                 string
	IssuerContactInfo            string
	WarehouseName                string
	WarehouseOperatorName        string
	WarehouseLicenseNumber       string
	WarehouseLocation            string
	WrNumber                     string
	WrIssueDate                  *time.Time
	WrExpiryDate                 *time.Time
	WrSystemRegistration         string
	WrRegistrationNumber         string
	WrVerifier                   string
	StorageCondition             string
	WarehouseAccreditationBody   string
	MinimumPurchaseAmount        string
	AutoRollover                 bool
	CurrentBeneficialOwner       string
	WrCustodianName              string
	OwnershipRightsRepresented   string
	TrusteeOrThirdPartyOversight string
	LienOrEncumbrances           string
	AssetValuation               string
	ValuationDate                *time.Time
	ValuationMethodology         string
	TokenizationObjective        string
	HoldingPeriodDays            int
	RedemptionMechanism          string
	PartyUnderwriter             string
	PartyAssetManager            string
	PartyLegalAdvisor            string
	PartyRegulator               string
	RiskMarket                   string
	RiskStorage                  string
	RiskTitle                    string
	RiskFraud                    string
	RiskInsurance                string
	RiskOperational              string
	RiskRegulatory               string
	RiskLiquidity                string
	RiskForceMajeure             string
	RiskEarlyRedemption          string
	MitigationMeasures           string
	InsuranceCoverageSummary     string
	InsuranceProvider            string
	InsuranceCoverageValue       string
}

// VaultFields covers vault-stored assets (bullion, collectibles, etc.),
// the same shape as CommodityFields for a non-warehouse-receipt custody
// arrangement.
type VaultFields struct {
	QualityStandard             string
	IssuerContactInformation    string
	VaultCustodianName          string
	VaultOperator               string
	VaultLicenseNumber          string
	VaultLocation               string
	Number                      string
	IssuerDate                  *time.Time
	ExpiryDate                  *time.Time
	RegistryRecord              string
	Verifier                    string
	StorageConditions           string
	VaultAccreditationBody      string
	OwnershipLegalHolder        string
	OwnershipCustodianName      string
	OwnershipTrustee            string
	OwnershipLienOrEncumbrances string
	ValuationAssetValuation     string
	HoldingLockInPeriodDays     int
	RiskMarket                  string
	RiskStorage                 string
	RiskTitle                   string
	RiskFraud                   string
	RiskInsurance               string
	RiskOperational             string
	RiskRegulatory              string
	RiskLiquidity               string
	RiskForceMajeure            string
	RiskEarlyRedemption         string
	MitigationMeasures          string
	InsuranceCoverageSummary    string
	InsuranceProvider           string
	CoverageValue               string
}

// IssuerDebtFields covers generic issuer/instrument/security/debt
// structuring details shared across several asset classes.
type IssuerDebtFields struct {
	IssuerRegistrationNo                   string
	SectorAndIndustry                      string
	LicenseOrPermitNumber                  string
	IssuerAdditionalInfo                   string
	IsinSerialNumber                       string
	EsgOrImpactMetrics                     string
	InstrumentAdditionalInfo               string
	SecurityType                           string
	CollateralDescription                  string
	CovenantSummary                        string
	CovenantTestingFrequency               string
	EventOfDefaultClauses                  string
	LegalEnforcementMechanism              string
	Guarantee                              string
	RecoveryEstimate                       string
	RiskProfileAdditionalInfo              string
	LicenseNumber                          string
	ExitLoadFee                            string
	EntryLoadFee                           string
	PortfolioLockInPeriodDays              int
	PortfolioPerformanceFee                string
	FundingStructure                       string // EQUITY, DEBT, HYBRID
	EquityPercentage                       string
	DebtPercentage                         string
	DebtInstrumentType                     string
	PrincipalPaymentMethod                 string
	DebtInstrumentRepaymentSource          string
	GuaranteesOrEnhancements               string
	DefaultAndRecoveryTerms                string
	RepaymentFrequency                     string
	InterestRepaymentFrequency             string
	DcsrDetails                            string
	SinkingFundStructure                   string
	CovenantMonitoringAgent                string
	RightOfRecourse                        string
	InterestRate                           string
	DcsrRatio                              string
	LtvRatio                               string
	InterestCoverageRatio                  string
	MaximumLeverageRatio                   string
	GracePeriodDays                        int
	TrusteeAppointed                       bool
	ReserveFundInPlace                     bool
	SecurityOrCollateralOffered            string
	FundInstrumentType                     string
	InstrumentRatingAgency                 string
	PortfolioTopHoldings                   string
	CreditRatingAgency                     string
	TrusteeRegNumber                       string
	CreditEnhancerOrGuarantor              string
	BondStructuringAdvisor                 string
	EntitiesAdditionalInfo                 string
	AuthorizedRepresentativeName           string
	AuthorizedRepresentativeTitle          string
	AuthorizedRepresentativeEmail          string
	AcceptTokenizationTermsAndAgreement    bool
	AttestInformationAccurateAndVerifiable bool
	AcknowledgedSuitabilityCriteria        bool
	MinimumKycTier                         string
	InvestorCategory                       string
	WithholdingTaxDisclosure               bool
	ExitWithFiat                           bool
}

// REITFields covers real-estate investment trusts.
type REITFields struct {
	TypeOfREIT                       string
	NavPerUnit                       string
	TotalUnitsOutstanding            string
	DistributionPolicy               string
	DebtOutstanding                  string
	ESGClassification                string
	PortfolioSummary                 string
	NoOfProperties                   int
	PropertyCategories               string
	GeographicDistribution           string
	TotalGrossAssetValue             string
	IndependentValuationFirm         string
	OccupancyRate                    string
	WeightedAverageLeaseExpiryMonths int
	AnnualRentalIncome               string
	NetOperatingIncome               string
	FundsFromOperations              string
	Top10Properties                  string
	Top10Tenants                     string
	TenantConcentrationRatio         string
	PropertyTypeAllocation           string
	GeographicAllocation             string
	DevelopmentProjects              string
	PartyRegistrar                   string
	PartyIndependentValuer           string
	ReitSponsor                      string
	OccupancyRisk                    string
	TenantConcentrationRisk          string
	RefinancingRisk                  string
}

// PrivateEquityFields covers private-equity/hybrid/yield-instrument funds.
type PrivateEquityFields struct {
	TypeOfFund                     string
	InvestmentStrategy             string
	Structure                      string
	TargetReturnRange              string
	PrivateEquityAllocation        string
	YieldInstrumentAllocation      string
	CashAllocation                 string
	WeightedPortfolioYield         string
	AveragePortfolioDurationMonths int
	AverageHoldingPeriodMonths     int
	PortfolioGrowthStrategy        string
	ExitStrategy                   string

	// Portfolio-company-holding (repeatable in the original; kept as a
	// single most-recent-holding summary here, matching the shape the
	// original's own struct declared rather than a separate has-many table
	// it never defined either).
	PCHCompanyName          string
	PCHIndustry             string
	PCHCountry              string
	PCHInvestmentDate       string
	PCHOwnershipPercentage  string
	PCHCostBasis            string
	PCHCurrentFairValue     string
	PCHRevenueGrowthRate    string
	PCHEBITDAGrowthRate     string
	PCHExitStatus           string
	PCHExpectedExitTimeline string

	YIHIssuer                     string
	PrincipalAmount               string
	CurrentMarketValue            string
	YIHDurationMonths             int
	LiquidityClassification       string
	PartyInvestmentCommittee      string
	PartyRiskCommittee            string
	AssetPerformanceRisk          string
	CashFlowVolatilityRisk        string
	CounterpartyConcentrationRisk string
	ValuationRisk                 string
	DefaultRecoveryRisk           string

	TopEquityHoldingsList    string
	TargetAllocation         string
	AllowedAllocationRange   string
	AssetClassesIncluded     string
	RebalancingFrequency     string
	BenchmarkIndexComposite  string
	TopDebtHoldings          string
	CreditRatingDistribution string
}
