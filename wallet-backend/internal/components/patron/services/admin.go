package services

import (
	"errors"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/patron/models"
)

// CreatePackageInput/CreateTierInput are the admin-provided shapes of a
// new package/tier - PriorityOrder controls upgrade-qualification
// ordering (validateUpgrade in services.go), lower is more exclusive.
type CreatePackageInput struct {
	ID            string
	Description   string
	PriorityOrder int
}

func (s *Service) CreatePackage(input CreatePackageInput) (*models.PatronPackage, error) {
	if input.ID == "" {
		return nil, apperrors.BadRequest("id is required")
	}
	pkg := models.PatronPackage{ID: input.ID, Description: input.Description, PriorityOrder: input.PriorityOrder}
	if err := s.DB.Create(&pkg).Error; err != nil {
		return nil, apperrors.Conflict("a package with this id already exists")
	}
	return &pkg, nil
}

func (s *Service) SetPackageInactive(id string, inactive bool) (*models.PatronPackage, error) {
	var pkg models.PatronPackage
	if err := s.DB.First(&pkg, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("package not found")
		}
		return nil, apperrors.Internal("failed to load package")
	}
	pkg.Inactive = inactive
	if err := s.DB.Save(&pkg).Error; err != nil {
		return nil, apperrors.Internal("failed to update package")
	}
	return &pkg, nil
}

func (s *Service) DeletePackage(id string) error {
	res := s.DB.Delete(&models.PatronPackage{}, "id = ?", id)
	if res.Error != nil {
		return apperrors.Internal("failed to delete package")
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("package not found")
	}
	return nil
}

type CreateTierInput struct {
	ID            string
	CanExpire     bool
	PriorityOrder int
}

func (s *Service) CreateTier(input CreateTierInput) (*models.PatronTier, error) {
	if input.ID == "" {
		return nil, apperrors.BadRequest("id is required")
	}
	tier := models.PatronTier{ID: input.ID, CanExpire: input.CanExpire, PriorityOrder: input.PriorityOrder}
	if err := s.DB.Create(&tier).Error; err != nil {
		return nil, apperrors.Conflict("a tier with this id already exists")
	}
	return &tier, nil
}

func (s *Service) SetTierInactive(id string, inactive bool) (*models.PatronTier, error) {
	var tier models.PatronTier
	if err := s.DB.First(&tier, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("tier not found")
		}
		return nil, apperrors.Internal("failed to load tier")
	}
	tier.Inactive = inactive
	if err := s.DB.Save(&tier).Error; err != nil {
		return nil, apperrors.Internal("failed to update tier")
	}
	return &tier, nil
}

func (s *Service) DeleteTier(id string) error {
	res := s.DB.Delete(&models.PatronTier{}, "id = ?", id)
	if res.Error != nil {
		return apperrors.Internal("failed to delete tier")
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("tier not found")
	}
	return nil
}

// UpsertMembershipGrade creates or repriced a (package, tier) grade - the
// USD price a subscribe/upgrade quote is computed from (subscribe.go's
// quote()). Upserts on the (PatronPackageID, PatronTierID) unique index
// rather than requiring a separate create-vs-update call, since an
// operator repricing an existing grade is the common case.
func (s *Service) UpsertMembershipGrade(packageID, tierID string, priceUSD float64) (*models.PatronMembershipGrade, error) {
	if packageID == "" || tierID == "" {
		return nil, apperrors.BadRequest("patronPackageId and patronTierId are required")
	}
	var count int64
	s.DB.Model(&models.PatronPackage{}).Where("id = ?", packageID).Count(&count)
	if count == 0 {
		return nil, apperrors.BadRequest("unknown package: " + packageID)
	}
	s.DB.Model(&models.PatronTier{}).Where("id = ?", tierID).Count(&count)
	if count == 0 {
		return nil, apperrors.BadRequest("unknown tier: " + tierID)
	}

	var grade models.PatronMembershipGrade
	err := s.DB.Where("patron_package_id = ? AND patron_tier_id = ?", packageID, tierID).First(&grade).Error
	if err == nil {
		grade.PriceUSD = priceUSD
		if err := s.DB.Save(&grade).Error; err != nil {
			return nil, apperrors.Internal("failed to update membership grade")
		}
		return &grade, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for an existing membership grade")
	}

	grade = models.PatronMembershipGrade{PatronPackageID: packageID, PatronTierID: tierID, PriceUSD: priceUSD}
	if err := s.DB.Create(&grade).Error; err != nil {
		return nil, apperrors.Internal("failed to create membership grade")
	}
	return &grade, nil
}

func (s *Service) DeleteMembershipGrade(id uint) error {
	res := s.DB.Delete(&models.PatronMembershipGrade{}, id)
	if res.Error != nil {
		return apperrors.Internal("failed to delete membership grade")
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("membership grade not found")
	}
	return nil
}

// SetPaymentAssetAllowed adds (or re-enables) a CuratedToken symbol to the
// subscription payment-asset allow-list, or disables an existing one -
// mirrors tokenization's TokenizationCurrency allow-list pattern (§4.9).
func (s *Service) SetPaymentAssetAllowed(symbol string, inactive bool) (*models.PatronSubscriptionPaymentAsset, error) {
	if symbol == "" {
		return nil, apperrors.BadRequest("symbol is required")
	}
	var asset models.PatronSubscriptionPaymentAsset
	err := s.DB.Where("symbol = ?", symbol).First(&asset).Error
	if err == nil {
		asset.Inactive = inactive
		if err := s.DB.Save(&asset).Error; err != nil {
			return nil, apperrors.Internal("failed to update payment asset")
		}
		return &asset, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for an existing payment asset")
	}
	asset = models.PatronSubscriptionPaymentAsset{Symbol: symbol, Inactive: inactive}
	if err := s.DB.Create(&asset).Error; err != nil {
		return nil, apperrors.Internal("failed to create payment asset")
	}
	return &asset, nil
}
