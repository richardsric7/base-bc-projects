// Package controllers wires HTTP routes to the servicelinks services. See
// PLAN.md §4.11 for the full design and the authorization bugs found in
// the original and fixed here. Route registration and the admin
// service-link-management surface live here; the partner-facing API-key-
// authenticated surface lives in approvals.go, users.go, payments.go,
// tokenization.go and documents.go.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	assetsServices "wallet-backend/internal/components/assets/services"
	paymentsServices "wallet-backend/internal/components/payments/services"
	"wallet-backend/internal/components/servicelinks/models"
	"wallet-backend/internal/components/servicelinks/services"
	tokenizationServices "wallet-backend/internal/components/tokenization/services"
	usersServices "wallet-backend/internal/components/users/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the servicelinks component's routes on router. It takes
// the already-constructed users/payments/assets/tokenization services
// directly (see services.go's package doc comment on why this component
// imports them rather than using a cross-component hook) so main.go must
// call this after those components' own Init functions.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig, users *usersServices.Service, payments *paymentsServices.Service, assets *assetsServices.Service, tokenization *tokenizationServices.Service) *services.Service {
	svc := services.New(gc.DB, users, payments, assets, tokenization, gc.Storage, gc.Push, gc.JWTSecret, gc.JWTExpiry, gc.ServiceLinkApprovalTTL)

	// Wallet-session-authenticated: the end user viewing/deciding on a
	// pending consent request a partner asked them to approve.
	walletAuthed := router.Group("/v1/approvals")
	walletAuthed.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))
	walletAuthed.GET("/:id", getApprovalForUser(svc))
	walletAuthed.POST("/:id/approve", approveForUser(svc))

	// Partner-facing: API-key-authenticated, capability-gated per route.
	partner := router.Group("/v1/partner")
	partner.Use(middleware.APIKeyAuth(gc.DB))
	registerApprovalRoutes(partner, svc)
	registerUserRoutes(partner, svc)
	registerPaymentRoutes(partner, svc)
	registerTokenizationRoutes(partner, svc)
	registerDocumentRoutes(partner, svc)

	// Admin: provisioning and lifecycle management of service links
	// themselves - the same admin-JWT mechanism established for the
	// tokenization and announcements admin surfaces (PLAN.md §4.9's
	// "Routes" note).
	admin := router.Group("/v1/admin/servicelinks")
	admin.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceAdmin))
	admin.POST("", createServiceLink(svc))
	admin.GET("", listServiceLinks(svc))
	admin.GET("/:id", getServiceLink(svc))
	admin.POST("/:id/verify", verifyServiceLink(svc))
	admin.POST("/:id/suspend", suspendServiceLink(svc))
	admin.POST("/:id/reactivate", reactivateServiceLink(svc))

	return svc
}

// currentServiceLink returns the ServiceLink attached by middleware.APIKeyAuth.
func currentServiceLink(c *gin.Context) *models.ServiceLink {
	return c.MustGet(middleware.CtxServiceLink).(*models.ServiceLink)
}

// requireCapability aborts with 403 and returns false unless the calling
// service link was granted the given capability - the granular per-route
// gate that replaces the original's single overloaded CreateUsersPermission
// flag (PLAN.md §4.11 finding 4).
func requireCapability(c *gin.Context, granted bool) bool {
	if !granted {
		apperrors.Abort(c, apperrors.Forbidden("your service link does not have this capability"))
		return false
	}
	return true
}

type createServiceLinkRequest struct {
	OwnerUserID                     uint   `json:"ownerUserId" binding:"required"`
	ShortName                       string `json:"shortName" binding:"required"`
	LongName                        string `json:"longName"`
	CanLogin                        bool   `json:"canLogin"`
	CanRequestAuthorization         bool   `json:"canRequestAuthorization"`
	CanRegisterEvents               bool   `json:"canRegisterEvents"`
	CanViewUserInfo                 bool   `json:"canViewUserInfo"`
	CanSendPushNotifications        bool   `json:"canSendPushNotifications"`
	CanLookupTokenInfo              bool   `json:"canLookupTokenInfo"`
	CanCreateUsers                  bool   `json:"canCreateUsers"`
	CanUpdateKYC                    bool   `json:"canUpdateKyc"`
	CanSendPayments                 bool   `json:"canSendPayments"`
	CanReadBalances                 bool   `json:"canReadBalances"`
	CanManageTokenization           bool   `json:"canManageTokenization"`
	AllowReferralForRegisteredUsers bool   `json:"allowReferralForRegisteredUsers"`
}

// createServiceLink returns the raw API key exactly once, in this response
// only - it is never retrievable again (PLAN.md §4.11 finding 1).
func createServiceLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createServiceLinkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("ownerUserId and shortName are required"))
			return
		}
		link, rawKey, err := svc.CreateServiceLink(services.CreateServiceLinkInput{
			OwnerUserID:                     req.OwnerUserID,
			ShortName:                       req.ShortName,
			LongName:                        req.LongName,
			CanLogin:                        req.CanLogin,
			CanRequestAuthorization:         req.CanRequestAuthorization,
			CanRegisterEvents:               req.CanRegisterEvents,
			CanViewUserInfo:                 req.CanViewUserInfo,
			CanSendPushNotifications:        req.CanSendPushNotifications,
			CanLookupTokenInfo:              req.CanLookupTokenInfo,
			CanCreateUsers:                  req.CanCreateUsers,
			CanUpdateKYC:                    req.CanUpdateKYC,
			CanSendPayments:                 req.CanSendPayments,
			CanReadBalances:                 req.CanReadBalances,
			CanManageTokenization:           req.CanManageTokenization,
			AllowReferralForRegisteredUsers: req.AllowReferralForRegisteredUsers,
		})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"serviceLink": link, "apiKey": rawKey})
	}
}

func listServiceLinks(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		links, err := svc.ListServiceLinks()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, links)
	}
}

func getServiceLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := uintParam(c, "id")
		if !ok {
			return
		}
		link, err := svc.GetServiceLink(id)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, link)
	}
}

func verifyServiceLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := uintParam(c, "id")
		if !ok {
			return
		}
		link, err := svc.VerifyServiceLink(id)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, link)
	}
}

type suspendServiceLinkRequest struct {
	Reason string `json:"reason"`
}

func suspendServiceLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := uintParam(c, "id")
		if !ok {
			return
		}
		var req suspendServiceLinkRequest
		_ = c.ShouldBindJSON(&req)
		link, err := svc.SuspendServiceLink(id, req.Reason)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, link)
	}
}

func reactivateServiceLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := uintParam(c, "id")
		if !ok {
			return
		}
		link, err := svc.ReactivateServiceLink(id)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, link)
	}
}
