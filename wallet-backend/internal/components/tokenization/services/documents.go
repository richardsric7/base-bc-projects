package services

import (
	"fmt"
	"io"
	"time"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
)

// UploadDocument stores a supporting document for an application and
// upserts its AssetTokenizationDocument row, keyed by (asset, type, title)
// exactly as upstream. requireOwnership is false for the admin upload path.
func (s *Service) UploadDocument(userID, assetID uint, requireOwnership bool, documentType, documentTitle string, file io.Reader, fileName string) (*models.AssetTokenizationDocument, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if requireOwnership && asset.InitiatorUserID != userID {
		return nil, apperrors.Forbidden("you may only upload documents for your own application")
	}

	key := fmt.Sprintf("tokenization/%d/documents/%d-%s", assetID, time.Now().UnixNano(), fileName)
	url, err := s.Storage.Put(key, file)
	if err != nil {
		return nil, apperrors.Internal("failed to store document")
	}

	var doc models.AssetTokenizationDocument
	err = s.DB.Where("tokenized_asset_id = ? AND document_type = ? AND document_title = ?", assetID, documentType, documentTitle).First(&doc).Error
	if err == nil {
		doc.DocumentURL = url
		if saveErr := s.DB.Save(&doc).Error; saveErr != nil {
			return nil, apperrors.Internal("failed to update document")
		}
		return &doc, nil
	}

	doc = models.AssetTokenizationDocument{
		TokenizedAssetID: assetID,
		DocumentType:     documentType,
		DocumentTitle:    documentTitle,
		DocumentURL:      url,
	}
	if err := s.DB.Create(&doc).Error; err != nil {
		return nil, apperrors.Internal("failed to save document")
	}
	return &doc, nil
}

// DeleteDocument removes an uploaded document by ID.
func (s *Service) DeleteDocument(userID uint, requireOwnership bool, documentID uint) error {
	var doc models.AssetTokenizationDocument
	if err := s.DB.First(&doc, documentID).Error; err != nil {
		return apperrors.NotFound("document not found")
	}
	if requireOwnership {
		asset, err := s.getAsset(doc.TokenizedAssetID)
		if err != nil {
			return err
		}
		if asset.InitiatorUserID != userID {
			return apperrors.Forbidden("you may only delete documents for your own application")
		}
	}
	if err := s.DB.Delete(&doc).Error; err != nil {
		return apperrors.Internal("failed to delete document")
	}
	return nil
}

// UploadLogo stores the asset's logo image, replacing any previous one.
func (s *Service) UploadLogo(userID, assetID uint, requireOwnership bool, file io.Reader, fileName string) (*models.TokenizedAsset, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if requireOwnership && asset.InitiatorUserID != userID {
		return nil, apperrors.Forbidden("you may only update your own application's logo")
	}
	key := fmt.Sprintf("tokenization/%d/logo-%d-%s", assetID, time.Now().UnixNano(), fileName)
	url, err := s.Storage.Put(key, file)
	if err != nil {
		return nil, apperrors.Internal("failed to store logo")
	}
	asset.AssetLogo = url
	if err := s.DB.Save(asset).Error; err != nil {
		return nil, apperrors.Internal("failed to save logo")
	}
	return asset, nil
}

// UploadFeeProofOfPayment stores proof that the tokenization fee (or
// application fee) was paid via an off-chain rail (bank transfer, etc.).
func (s *Service) UploadFeeProofOfPayment(userID, assetID uint, paymentMethodID, transactionReference string, file io.Reader, fileName string) (*models.TokenizationFeeProofOfPayment, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.InitiatorUserID != userID {
		return nil, apperrors.Forbidden("you may only upload proof of payment for your own application")
	}
	key := fmt.Sprintf("tokenization/%d/fee-proof/%d-%s", assetID, time.Now().UnixNano(), fileName)
	url, err := s.Storage.Put(key, file)
	if err != nil {
		return nil, apperrors.Internal("failed to store proof of payment")
	}
	proof := models.TokenizationFeeProofOfPayment{
		TokenizedAssetID:               assetID,
		TokenizationFeePaymentMethodID: paymentMethodID,
		TransactionReference:           transactionReference,
		DocumentURL:                    url,
	}
	if err := s.DB.Create(&proof).Error; err != nil {
		return nil, apperrors.Internal("failed to save proof of payment")
	}
	return &proof, nil
}

// DeleteFeeProofOfPayment removes an uploaded proof-of-payment record.
func (s *Service) DeleteFeeProofOfPayment(userID uint, documentID uint) error {
	var proof models.TokenizationFeeProofOfPayment
	if err := s.DB.First(&proof, documentID).Error; err != nil {
		return apperrors.NotFound("proof of payment not found")
	}
	asset, err := s.getAsset(proof.TokenizedAssetID)
	if err != nil {
		return err
	}
	if asset.InitiatorUserID != userID {
		return apperrors.Forbidden("you may only delete proof of payment for your own application")
	}
	if err := s.DB.Delete(&proof).Error; err != nil {
		return apperrors.Internal("failed to delete proof of payment")
	}
	return nil
}
