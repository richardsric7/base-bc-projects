package models

import "time"

// AssetTokenizationDocument is one uploaded supporting document (proof of
// ownership, valuation certificate, etc.) - direct port, chain-agnostic.
type AssetTokenizationDocument struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	CreatedAt        time.Time `json:"createdAt"`
	TokenizedAssetID uint      `gorm:"index;not null" json:"tokenizedAssetId"`
	DocumentType     string    `gorm:"size:128;not null" json:"documentType"`
	DocumentTitle    string    `gorm:"size:255;not null" json:"documentTitle"`
	DocumentURL      string    `gorm:"not null" json:"documentUrl"`
	ShowToPublic     bool      `gorm:"default:false" json:"showToPublic"`
}

// AssetTokenizationDocumentType is the reference catalog of accepted
// document types - direct port of upstream's 25 known codes (proof of
// asset existence/ownership/condition, custodian agreement, valuation
// certificate, SEC registration, environmental compliance, etc.).
type AssetTokenizationDocumentType struct {
	ID                      uint   `gorm:"primaryKey" json:"id"`
	DocumentType            string `gorm:"size:128;uniqueIndex;not null" json:"documentType"`
	DocumentTypeDescription string `json:"documentTypeDescription"`
	DocumentCategory        string `gorm:"size:64" json:"documentCategory"`
	IsPublic                bool   `gorm:"default:false" json:"isPublic"`
}

// TokenizationFeeProofOfPayment is an uploaded proof of the application/
// tokenization fee payment.
type TokenizationFeeProofOfPayment struct {
	ID                             uint      `gorm:"primaryKey" json:"id"`
	CreatedAt                      time.Time `json:"createdAt"`
	TokenizedAssetID               uint      `gorm:"index;not null" json:"tokenizedAssetId"`
	TokenizationFeePaymentMethodID string    `gorm:"size:32" json:"tokenizationFeePaymentMethodId"`
	TransactionReference           string    `json:"transactionReference"`
	DocumentURL                    string    `gorm:"not null" json:"documentUrl"`
}

// TokenizationFeePaymentMethod is the reference catalog of accepted fee
// payment rails (e.g. "DIGITAL_ASSET", "FIAT").
type TokenizationFeePaymentMethod struct {
	ID               string `gorm:"primaryKey;size:32" json:"id"`
	FeeDescription   string `json:"feeDescription"`
	ExtraDescription string `json:"extraDescription"`
	Inactive         bool   `gorm:"default:false" json:"-"`
}
