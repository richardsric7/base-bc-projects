package services

import (
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/payments/models"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"
)

type Service struct {
	DB         *gorm.DB
	Blockchain *network.Client
}

func New(db *gorm.DB, blockchain *network.Client) *Service {
	return &Service{DB: db, Blockchain: blockchain}
}

// BuildPaymentXDR returns an unsigned payment transaction for the source
// account's owner to sign client-side.
func (s *Service) BuildPaymentXDR(sourcePublicKey, destinationPublicKey, assetCode, assetIssuer, amount string) (string, error) {
	if !validators.IsValidStellarPublicKey(sourcePublicKey) || !validators.IsValidStellarPublicKey(destinationPublicKey) {
		return "", apperrors.BadRequest("invalid Stellar public key")
	}
	xdrString, err := s.Blockchain.BuildPaymentXDR(sourcePublicKey, destinationPublicKey, assetCode, assetIssuer, amount)
	if err != nil {
		return "", apperrors.BadRequest(err.Error())
	}
	return xdrString, nil
}

// SubmitPayment submits a client-signed payment transaction and records it
// in payment history.
func (s *Service) SubmitPayment(signedXDR, fromPublicKey, toPublicKey, assetCode, assetIssuer, amount string) (*models.PaymentHistory, error) {
	tx, err := s.Blockchain.SubmitSignedTransaction(signedXDR)
	if err != nil {
		return nil, apperrors.BadRequest("transaction rejected by the network: " + err.Error())
	}

	record := models.PaymentHistory{
		FromPublicKey: fromPublicKey,
		ToPublicKey:   toPublicKey,
		AssetCode:     assetCode,
		AssetIssuer:   assetIssuer,
		Amount:        amount,
		TxHash:        tx.Hash,
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return nil, apperrors.Internal("payment submitted but failed to record history")
	}
	return &record, nil
}

// History returns the payments a public key has sent or received, most
// recent first.
func (s *Service) History(publicKey string) ([]models.PaymentHistory, error) {
	var history []models.PaymentHistory
	err := s.DB.Where("from_public_key = ? OR to_public_key = ?", publicKey, publicKey).
		Order("created_at DESC").
		Find(&history).Error
	if err != nil {
		return nil, apperrors.Internal("failed to load payment history")
	}
	return history, nil
}
