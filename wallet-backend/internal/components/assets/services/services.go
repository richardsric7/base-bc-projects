package services

import (
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/assets/models"
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

// ListCurated returns the active entries in the curated-asset catalog.
func (s *Service) ListCurated() ([]models.CuratedAsset, error) {
	var assets []models.CuratedAsset
	if err := s.DB.Where("is_active = ?", true).Find(&assets).Error; err != nil {
		return nil, apperrors.Internal("failed to load curated assets")
	}
	return assets, nil
}

// BuildTrustlineXDR returns an unsigned ChangeTrust transaction for
// publicKey to sign client-side. limit="0" removes an existing trustline.
func (s *Service) BuildTrustlineXDR(publicKey, code, issuer, limit string) (string, error) {
	if !validators.IsValidStellarPublicKey(publicKey) {
		return "", apperrors.BadRequest("invalid Stellar public key")
	}
	if !validators.IsValidAssetCode(code) {
		return "", apperrors.BadRequest("invalid asset code")
	}
	xdrString, err := s.Blockchain.BuildChangeTrustXDR(publicKey, code, issuer, limit)
	if err != nil {
		return "", apperrors.BadRequest(err.Error())
	}
	return xdrString, nil
}

// SubmitSignedTransaction submits a client-signed trustline (or any other)
// transaction to the network and returns its hash.
func (s *Service) SubmitSignedTransaction(signedXDR string) (string, error) {
	tx, err := s.Blockchain.SubmitSignedTransaction(signedXDR)
	if err != nil {
		return "", apperrors.BadRequest("transaction rejected by the network: " + err.Error())
	}
	return tx.Hash, nil
}
