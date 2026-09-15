package services

import (
	"errors"

	"gorm.io/gorm"

	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/tokenization/models"
	usersModels "wallet-backend/internal/components/users/models"

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

// requireInitiatorOfPrivateOffering loads assetID and checks both that
// userID is its own applicant and that it's actually a PRIVATE offering -
// the shared precondition for every closed-group management call below.
// A PUBLIC offering has no ClosedGroupID to manage at all.
func (s *Service) requireInitiatorOfPrivateOffering(userID, assetID uint) (*models.TokenizedAsset, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.InitiatorUserID != userID {
		return nil, apperrors.Forbidden("you may only manage your own application's access group")
	}
	if asset.OfferingType != models.OfferingPrivate {
		return nil, apperrors.Conflict("only a private offering has a closed group to manage")
	}
	return asset, nil
}

// CreatePrivateOfferingGroup creates the ClosedGroup a PRIVATE offering's
// RequestMint guard requires (see minting.go) and points asset.ClosedGroupID
// at it. Unlike a sharedaccess wallet-access group, this one controls no
// funds and has no Safe of its own (Address stays nil, Threshold unused) -
// it exists purely as the allow-list enforceOfferingAccess/RequestMint
// check against, so creating it needs no on-chain transaction, matching
// the original's own closed-group creation (a plain application record,
// not a wallet). Idempotent-ish: calling it again before a group exists
// replaces the pending groupName; once ClosedGroupID is set, callers must
// go through AddPrivateOfferingMember/RemovePrivateOfferingMember instead
// of recreating the group out from under any members already added.
func (s *Service) CreatePrivateOfferingGroup(userID, assetID uint, groupName string) (*sharedaccessModels.ClosedGroup, error) {
	if groupName == "" {
		return nil, apperrors.BadRequest("groupName is required")
	}
	asset, err := s.requireInitiatorOfPrivateOffering(userID, assetID)
	if err != nil {
		return nil, err
	}
	if asset.ClosedGroupID != nil {
		return nil, apperrors.Conflict("this offering already has an access group")
	}
	initiator, err := s.getUserByID(userID)
	if err != nil {
		return nil, err
	}

	group := sharedaccessModels.ClosedGroup{
		Name:             groupName,
		Purpose:          sharedaccessModels.PurposePrivateOffering,
		CreatedByAddress: initiator.Address,
	}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&group).Error; err != nil {
			return apperrors.Internal("failed to create the access group")
		}
		asset.ClosedGroupID = &group.ID
		if err := tx.Save(asset).Error; err != nil {
			return apperrors.Internal("failed to link the access group to this offering")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &group, nil
}

// AddPrivateOfferingMember adds memberUsername to assetID's access group -
// the original's own "members are added by username only" convention
// (users.Service.ResolveUsernameToPrimaryWalletAddress's own doc comment),
// resolved here to the primary-wallet address enforceOfferingAccess
// actually checks a buyer against. Role is recorded as VIEW_ONLY: a
// private-offering group is a plain allow-list with no on-chain owner set
// and no approval threshold of its own (see CreatePrivateOfferingGroup),
// so GroupRole's initiate/approve distinction is meaningless here - a
// value is only required because GroupMember.Role is NOT NULL.
func (s *Service) AddPrivateOfferingMember(userID, assetID uint, memberUsername string) error {
	asset, err := s.requireInitiatorOfPrivateOffering(userID, assetID)
	if err != nil {
		return err
	}
	if asset.ClosedGroupID == nil {
		return apperrors.Conflict("create this offering's access group first")
	}
	var member usersModels.User
	if err := s.DB.Where("username = ?", memberUsername).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.NotFound("user not found")
		}
		return apperrors.Internal("failed to look up member")
	}

	var existing sharedaccessModels.GroupMember
	err = s.DB.Where("group_id = ? AND member_address = ?", *asset.ClosedGroupID, member.Address).First(&existing).Error
	if err == nil {
		return apperrors.Conflict(memberUsername + " is already a member of this access group")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.Internal("failed to check existing membership")
	}

	row := sharedaccessModels.GroupMember{
		GroupID:       *asset.ClosedGroupID,
		MemberAddress: member.Address,
		Role:          sharedaccessModels.RoleViewOnly,
	}
	if err := s.DB.Create(&row).Error; err != nil {
		return apperrors.Internal("failed to add member")
	}
	return nil
}

// RemovePrivateOfferingMember removes memberUsername from assetID's access
// group - the inverse of AddPrivateOfferingMember.
func (s *Service) RemovePrivateOfferingMember(userID, assetID uint, memberUsername string) error {
	asset, err := s.requireInitiatorOfPrivateOffering(userID, assetID)
	if err != nil {
		return err
	}
	if asset.ClosedGroupID == nil {
		return apperrors.Conflict("this offering has no access group yet")
	}
	var member usersModels.User
	if err := s.DB.Where("username = ?", memberUsername).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.NotFound("user not found")
		}
		return apperrors.Internal("failed to look up member")
	}

	result := s.DB.Where("group_id = ? AND member_address = ?", *asset.ClosedGroupID, member.Address).
		Delete(&sharedaccessModels.GroupMember{})
	if result.Error != nil {
		return apperrors.Internal("failed to remove member")
	}
	if result.RowsAffected == 0 {
		return apperrors.NotFound(memberUsername + " is not a member of this access group")
	}
	return nil
}

// ListPrivateOfferingMembers lists assetID's access-group members by
// username, for the applicant's own management UI. Any caller may list -
// this mirrors sharedaccess.ListMembers' own posture (membership
// visibility isn't itself sensitive), and unlike Add/Remove it's not
// gated to the initiator, so a prospective member can confirm they've
// been added.
func (s *Service) ListPrivateOfferingMembers(assetID uint) ([]string, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.OfferingType != models.OfferingPrivate || asset.ClosedGroupID == nil {
		return []string{}, nil
	}
	var members []sharedaccessModels.GroupMember
	if err := s.DB.Where("group_id = ?", *asset.ClosedGroupID).Find(&members).Error; err != nil {
		return nil, apperrors.Internal("failed to load members")
	}
	usernames := make([]string, 0, len(members))
	for _, m := range members {
		var user usersModels.User
		if err := s.DB.Where("address = ?", m.MemberAddress).First(&user).Error; err != nil {
			continue
		}
		usernames = append(usernames, user.Username)
	}
	return usernames, nil
}
