package services

import (
	"strings"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/models"
)

// CreateServiceLinkInput is the admin-provided shape of a new partner
// credential. Capabilities default to false - an admin grants exactly
// what a partner needs, never everything by default (PLAN.md §4.11
// finding 4: the original's single overloaded CreateUsersPermission made
// "everything or nothing" the only real choice).
type CreateServiceLinkInput struct {
	OwnerUserID                     uint
	ShortName                       string
	LongName                        string
	CanLogin                        bool
	CanRequestAuthorization         bool
	CanRegisterEvents               bool
	CanViewUserInfo                 bool
	CanSendPushNotifications        bool
	CanLookupTokenInfo              bool
	CanCreateUsers                  bool
	CanUpdateKYC                    bool
	CanSendPayments                 bool
	CanReadBalances                 bool
	CanManageTokenization           bool
	AllowReferralForRegisteredUsers bool
}

// CreateServiceLink provisions a new partner credential (admin-only - see
// controllers for the AudienceAdmin gate) and returns the raw API key
// exactly once; only its hash is ever persisted (PLAN.md §4.11 finding 1).
// New service links start unverified - an admin must separately verify
// them (VerifyServiceLink) before APIKeyAuth will accept requests using
// this key, matching the original's Verified flag but actually enforced
// this time (finding 3).
func (s *Service) CreateServiceLink(input CreateServiceLinkInput) (link *models.ServiceLink, rawAPIKey string, err error) {
	if strings.TrimSpace(input.ShortName) == "" {
		return nil, "", apperrors.BadRequest("shortName is required")
	}
	if _, err := s.Users.GetByID(input.OwnerUserID); err != nil {
		return nil, "", apperrors.BadRequest("unknown owner user")
	}

	raw, hash, err := generateAPIKey()
	if err != nil {
		return nil, "", err
	}

	entry := models.ServiceLink{
		OwnerUserID:                     input.OwnerUserID,
		APIKeyHash:                      hash,
		ShortName:                       input.ShortName,
		LongName:                        input.LongName,
		CanLogin:                        input.CanLogin,
		CanRequestAuthorization:         input.CanRequestAuthorization,
		CanRegisterEvents:               input.CanRegisterEvents,
		CanViewUserInfo:                 input.CanViewUserInfo,
		CanSendPushNotifications:        input.CanSendPushNotifications,
		CanLookupTokenInfo:              input.CanLookupTokenInfo,
		CanCreateUsers:                  input.CanCreateUsers,
		CanUpdateKYC:                    input.CanUpdateKYC,
		CanSendPayments:                 input.CanSendPayments,
		CanReadBalances:                 input.CanReadBalances,
		CanManageTokenization:           input.CanManageTokenization,
		AllowReferralForRegisteredUsers: input.AllowReferralForRegisteredUsers,
	}
	if err := s.DB.Create(&entry).Error; err != nil {
		return nil, "", apperrors.Conflict("a service link with this short name already exists")
	}
	return &entry, raw, nil
}

// VerifyServiceLink flips a service link's Verified flag - required
// before APIKeyAuth will accept any request using its key.
func (s *Service) VerifyServiceLink(id uint) (*models.ServiceLink, error) {
	link, err := s.getServiceLink(id)
	if err != nil {
		return nil, err
	}
	link.Verified = true
	if err := s.DB.Save(link).Error; err != nil {
		return nil, apperrors.Internal("failed to verify service link")
	}
	return link, nil
}

// SuspendServiceLink immediately blocks every request using this service
// link's key, regardless of which capabilities it was granted - checked
// centrally by middleware.APIKeyAuth (PLAN.md §4.11 finding 3).
func (s *Service) SuspendServiceLink(id uint, reason string) (*models.ServiceLink, error) {
	link, err := s.getServiceLink(id)
	if err != nil {
		return nil, err
	}
	link.Suspended = true
	link.SuspensionReason = reason
	if err := s.DB.Save(link).Error; err != nil {
		return nil, apperrors.Internal("failed to suspend service link")
	}
	return link, nil
}

// ReactivateServiceLink lifts a suspension.
func (s *Service) ReactivateServiceLink(id uint) (*models.ServiceLink, error) {
	link, err := s.getServiceLink(id)
	if err != nil {
		return nil, err
	}
	link.Suspended = false
	link.SuspensionReason = ""
	if err := s.DB.Save(link).Error; err != nil {
		return nil, apperrors.Internal("failed to reactivate service link")
	}
	return link, nil
}

// GetServiceLink fetches one service link by ID (admin use).
func (s *Service) GetServiceLink(id uint) (*models.ServiceLink, error) {
	return s.getServiceLink(id)
}

// ListServiceLinks lists every provisioned service link (admin use).
func (s *Service) ListServiceLinks() ([]models.ServiceLink, error) {
	var links []models.ServiceLink
	if err := s.DB.Order("created_at DESC").Find(&links).Error; err != nil {
		return nil, apperrors.Internal("failed to load service links")
	}
	return links, nil
}
