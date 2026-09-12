package controllers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/services"
)

// registerAdminRoutes registers the staff/admin surface - vetting, fee
// acknowledgement, minting-request review (see minting.go, shared with
// the authed group since staff also authenticate with a wallet-session
// JWT to sign their own mint approvals), sales-date management, and the
// no-ownership-check delete variant. Gated by middleware.AudienceAdmin,
// the same admin-JWT mechanism the announcements component established -
// see PLAN.md §4.9's "Routes" note for why this doesn't need to wait on
// Phase 12's admin surface.
func registerAdminRoutes(admin *gin.RouterGroup, svc *services.Service) {
	admin.PUT("/:assetId/vet", vetApplication(svc))
	admin.POST("/:assetId/fail-due-diligence", failDueDiligence(svc))
	admin.POST("/:assetId/acknowledge-fee", acknowledgeFeePayment(svc))
	admin.PUT("/:assetId/sales-dates", updateSalesDates(svc))
	admin.DELETE("/:assetId", adminDeleteApplication(svc))
}

type vetApplicationRequest struct {
	ApprovedAssetCustodianID      uint   `json:"approvedAssetCustodianId" binding:"required"`
	CustodianFeePercent           string `json:"custodianFeePercent"`
	CustodianFeeFixed             string `json:"custodianFeeFixed"`
	AssetManagerID                uint   `json:"assetManagerId" binding:"required"`
	AssetManagerFeePercent        string `json:"assetManagerFeePercent"`
	AssetManagerFeeFixed          string `json:"assetManagerFeeFixed"`
	AssetIssuingHouseID           uint   `json:"assetIssuingHouseId" binding:"required"`
	IssuingHouseFeePercent        string `json:"issuingHouseFeePercent"`
	IssuingHouseFeeFixed          string `json:"issuingHouseFeeFixed"`
	LegalAndProfessionalPartnerID uint   `json:"legalAndProfessionalPartnerId" binding:"required"`
	LegalPartnerFeePercent        string `json:"legalPartnerFeePercent"`
	LegalPartnerFeeFixed          string `json:"legalPartnerFeeFixed"`
	RatingAgencyID                uint   `json:"ratingAgencyId" binding:"required"`
	RatingAgencyFeePercent        string `json:"ratingAgencyFeePercent"`
	RatingAgencyFeeFixed          string `json:"ratingAgencyFeeFixed"`
	TrusteeID                     uint   `json:"trusteeId" binding:"required"`
	TrusteeFeePercent             string `json:"trusteeFeePercent"`
	TrusteeFeeFixed               string `json:"trusteeFeeFixed"`
	LegalAdviserID                uint   `json:"legalAdviserId"`
	FinancialAdviserID            uint   `json:"financialAdviserId"`
	ProceedPayoutCurrency         string `json:"proceedPayoutCurrency"`
	AssetQuoteCurrency            string `json:"assetQuoteCurrency"`
	ExcludeSecFee                 bool   `json:"excludeSecFee"`
}

func vetApplication(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req vetApplicationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("all stakeholder ids are required"))
			return
		}
		asset, err := svc.Vet(assetID, services.VetInput{
			ApprovedAssetCustodianID:      req.ApprovedAssetCustodianID,
			CustodianFeePercent:           req.CustodianFeePercent,
			CustodianFeeFixed:             req.CustodianFeeFixed,
			AssetManagerID:                req.AssetManagerID,
			AssetManagerFeePercent:        req.AssetManagerFeePercent,
			AssetManagerFeeFixed:          req.AssetManagerFeeFixed,
			AssetIssuingHouseID:           req.AssetIssuingHouseID,
			IssuingHouseFeePercent:        req.IssuingHouseFeePercent,
			IssuingHouseFeeFixed:          req.IssuingHouseFeeFixed,
			LegalAndProfessionalPartnerID: req.LegalAndProfessionalPartnerID,
			LegalPartnerFeePercent:        req.LegalPartnerFeePercent,
			LegalPartnerFeeFixed:          req.LegalPartnerFeeFixed,
			RatingAgencyID:                req.RatingAgencyID,
			RatingAgencyFeePercent:        req.RatingAgencyFeePercent,
			RatingAgencyFeeFixed:          req.RatingAgencyFeeFixed,
			TrusteeID:                     req.TrusteeID,
			TrusteeFeePercent:             req.TrusteeFeePercent,
			TrusteeFeeFixed:               req.TrusteeFeeFixed,
			LegalAdviserID:                req.LegalAdviserID,
			FinancialAdviserID:            req.FinancialAdviserID,
			ProceedPayoutCurrency:         req.ProceedPayoutCurrency,
			AssetQuoteCurrency:            req.AssetQuoteCurrency,
			ExcludeSecFee:                 req.ExcludeSecFee,
		})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

type failDueDiligenceRequest struct {
	Reason string `json:"reason" binding:"required"`
}

func failDueDiligence(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req failDueDiligenceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("a reason of at least 5 characters is required"))
			return
		}
		asset, err := svc.FailDueDiligence(assetID, req.Reason)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

func acknowledgeFeePayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		asset, err := svc.AcknowledgeFeePayment(assetID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

type updateSalesDatesRequest struct {
	SalesStart string `json:"salesStart" binding:"required"` // YYYY-MM-DD
	SalesEnd   string `json:"salesEnd" binding:"required"`
}

func updateSalesDates(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req updateSalesDatesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("salesStart and salesEnd (YYYY-MM-DD) are required"))
			return
		}
		salesStart, err := time.Parse("2006-01-02", req.SalesStart)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("salesStart must be in YYYY-MM-DD format"))
			return
		}
		salesEnd, err := time.Parse("2006-01-02", req.SalesEnd)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("salesEnd must be in YYYY-MM-DD format"))
			return
		}
		asset, err := svc.UpdateSalesDates(assetID, salesStart, salesEnd)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

func adminDeleteApplication(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		if err := svc.Delete(0, assetID, false); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
