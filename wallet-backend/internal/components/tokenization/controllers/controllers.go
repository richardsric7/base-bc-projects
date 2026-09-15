// Package controllers wires HTTP routes to the tokenization services. See
// PLAN.md §4.9 for the full route table and design; this file covers
// route registration, the application lifecycle, and reference data.
// Document/logo/fee-proof uploads live in documents.go, minting in
// minting.go, purchasing/interest/early-exit in purchases.go, and the
// admin/vetting surface in admin.go.
package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/components/tokenization/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the tokenization component's routes on router and
// returns the underlying Service so main.go can run its background
// sales-activation workers.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.Blockchain, gc.Storage, gc.TokenizationIssuerKeySalt, gc.TokenizationDistributionKeySalt, gc.TokenizationTokenLimit)

	public := router.Group("/v1/tokenization/public")
	public.GET("", getPublicReferenceData(svc))

	authed := router.Group("/v1/tokenization")
	authed.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))
	authed.GET("/reference", getFullReferenceData(svc))
	authed.GET("/types", getTypesBySubSector(svc))
	authed.POST("", submitApplication(svc))
	authed.GET("/applications", listMyApplications(svc))
	authed.GET("/:assetId", getApplication(svc))
	authed.PUT("/:assetId/confirm", confirmApplication(svc))
	authed.POST("/:assetId/confirm/pay-fee", submitApplicationFee(svc))
	authed.POST("/:assetId/fee/confirm", confirmFeePayment(svc))
	authed.DELETE("/:assetId", deleteApplication(svc))

	registerDocumentRoutes(authed, svc)
	registerMintingRoutes(authed, svc)
	registerPurchaseRoutes(authed, svc)

	admin := router.Group("/v1/admin/tokenization")
	admin.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceAdmin))
	registerAdminRoutes(admin, svc)

	return svc
}

// resolveUserID maps the verified session address to a users.User.ID,
// aborting the request and returning ok=false on failure - every handler
// below starts with this.
func resolveUserID(c *gin.Context, svc *services.Service) (uint, bool) {
	userID, err := svc.ResolveUserID(c.GetString(middleware.CtxSubject))
	if err != nil {
		apperrors.AbortAny(c, err)
		return 0, false
	}
	return userID, true
}

// assetIDParam parses the :assetId path parameter.
func assetIDParam(c *gin.Context) (uint, bool) {
	return idParam(c, "assetId")
}

// idParam parses a uint path parameter by name, aborting with a 400 on
// failure.
func idParam(c *gin.Context, name string) (uint, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil {
		apperrors.Abort(c, apperrors.BadRequest("invalid "+name))
		return 0, false
	}
	return uint(id), true
}

func getPublicReferenceData(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := svc.PublicReferenceData()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, data)
	}
}

func getFullReferenceData(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := svc.FullReferenceData()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, data)
	}
}

func getTypesBySubSector(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		types, err := svc.TypesBySubSector(c.Query("subSectorId"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, types)
	}
}

func submitApplication(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		var body models.TokenizedAsset
		if err := c.ShouldBindJSON(&body); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("invalid application payload"))
			return
		}
		asset, err := svc.SubmitApplication(userID, &body)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

func listMyApplications(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assets, err := svc.ListByInitiator(userID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, assets)
	}
}

func getApplication(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		asset, err := svc.GetByID(assetID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

func confirmApplication(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		signer := c.GetString(middleware.CtxSigner)
		asset, proposal, err := svc.ConfirmApplication(c.Request.Context(), userID, assetID, signer)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"asset": asset, "feePayment": proposal})
	}
}

type submitApplicationFeeRequest struct {
	ActionID  uint   `json:"actionId" binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

func submitApplicationFee(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := resolveUserID(c, svc); !ok {
			return
		}
		var req submitApplicationFeeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("actionId and signature are required"))
			return
		}
		signer := c.GetString(middleware.CtxSigner)
		if err := svc.SubmitApplicationFee(c.Request.Context(), req.ActionID, signer, req.Signature); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func confirmFeePayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		asset, err := svc.ConfirmFeePayment(userID, assetID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

func deleteApplication(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		if err := svc.Delete(userID, assetID, true); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
