package services

import (
	"errors"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
)

// ExpressInterest upserts a waitlist entry. Gated on Status == Minted -
// upstream's own literal (if slightly non-obvious) gate: interest can be
// logged once an asset is minted and awaiting its sale window, not before
// and not after the primary sale has already opened. Preserved as-is
// rather than "fixed", since it's documented, intentional upstream
// behavior, not a bug (see PLAN.md §4.9).
func (s *Service) ExpressInterest(userID, assetID uint, amount string) (*models.ExpressionOfInterest, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.Status != models.StatusMinted {
		return nil, apperrors.Conflict("this asset is not currently accepting expressions of interest")
	}

	var interest models.ExpressionOfInterest
	err = s.DB.Where("tokenized_asset_id = ? AND user_id = ?", assetID, userID).First(&interest).Error
	if err == nil {
		interest.Amount = amount
		if err := s.DB.Save(&interest).Error; err != nil {
			return nil, apperrors.Internal("failed to update expression of interest")
		}
		return &interest, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for an existing expression of interest")
	}

	interest = models.ExpressionOfInterest{TokenizedAssetID: assetID, UserID: userID, Amount: amount}
	if err := s.DB.Create(&interest).Error; err != nil {
		return nil, apperrors.Internal("failed to record expression of interest")
	}
	return &interest, nil
}

// ListExpressionsOfInterest lists everyone who's expressed interest in one asset.
func (s *Service) ListExpressionsOfInterest(assetID uint) ([]models.ExpressionOfInterest, error) {
	var interests []models.ExpressionOfInterest
	if err := s.DB.Where("tokenized_asset_id = ?", assetID).Find(&interests).Error; err != nil {
		return nil, apperrors.Internal("failed to load expressions of interest")
	}
	return interests, nil
}

// ListMyExpressionsOfInterest lists one user's own waitlist entries.
func (s *Service) ListMyExpressionsOfInterest(userID uint) ([]models.ExpressionOfInterest, error) {
	var interests []models.ExpressionOfInterest
	if err := s.DB.Where("user_id = ?", userID).Find(&interests).Error; err != nil {
		return nil, apperrors.Internal("failed to load expressions of interest")
	}
	return interests, nil
}
