package services

import (
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/tokenization/models"

	"wallet-backend/internal/apperrors"
)

// enforceOfferingAccess gates a PRIVATE offering to its ClosedGroup's
// members - reusing sharedaccess's ClosedGroup/GroupMember tables with
// Purpose PRIVATE_OFFERING (PLAN.md §4.2/§4.9) as a plain membership
// allow-list, rather than a parallel table. A PUBLIC offering has nothing
// to check here. Read directly against sharedaccess's models (not its
// services), the same cross-component-models pattern this component
// already uses for assets.CuratedToken and users.User.
func (s *Service) enforceOfferingAccess(asset *models.TokenizedAsset, buyerAddress string) error {
	if asset.OfferingType != models.OfferingPrivate {
		return nil
	}
	if asset.ClosedGroupID == nil {
		return apperrors.Conflict("this private offering has no configured access group")
	}
	var count int64
	s.DB.Model(&sharedaccessModels.GroupMember{}).
		Where("group_id = ? AND member_address = ?", *asset.ClosedGroupID, buyerAddress).
		Count(&count)
	if count == 0 {
		return apperrors.Forbidden("you are not a member of this private offering's access group")
	}
	return nil
}
