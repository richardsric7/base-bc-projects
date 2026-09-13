package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/patron/services"
)

// registerAdminRoutes registers patron-config management - packages,
// tiers, membership-grade pricing, and the subscription payment-asset
// allow-list. Gated by middleware.AudienceAdmin, same as every other
// admin surface in this port - see PLAN.md §4.12.
func registerAdminRoutes(admin *gin.RouterGroup, svc *services.Service) {
	admin.POST("/packages", createPackage(svc))
	admin.PUT("/packages/:id/inactive", setPackageInactive(svc))
	admin.DELETE("/packages/:id", deletePackage(svc))
	admin.POST("/tiers", createTier(svc))
	admin.PUT("/tiers/:id/inactive", setTierInactive(svc))
	admin.DELETE("/tiers/:id", deleteTier(svc))
	admin.PUT("/grades", upsertMembershipGrade(svc))
	admin.DELETE("/grades/:id", deleteMembershipGrade(svc))
	admin.PUT("/payment-assets/:symbol", setPaymentAssetAllowed(svc))
}

type createPackageRequest struct {
	ID            string `json:"id"`
	Description   string `json:"description"`
	PriorityOrder int    `json:"priorityOrder"`
}

func createPackage(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createPackageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("id is required"))
			return
		}
		pkg, err := svc.CreatePackage(services.CreatePackageInput{ID: req.ID, Description: req.Description, PriorityOrder: req.PriorityOrder})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, pkg)
	}
}

type setInactiveRequest struct {
	Inactive bool `json:"inactive"`
}

func setPackageInactive(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req setInactiveRequest
		_ = c.ShouldBindJSON(&req)
		pkg, err := svc.SetPackageInactive(c.Param("id"), req.Inactive)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, pkg)
	}
}

func deletePackage(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.DeletePackage(c.Param("id")); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type createTierRequest struct {
	ID            string `json:"id"`
	CanExpire     bool   `json:"canExpire"`
	PriorityOrder int    `json:"priorityOrder"`
}

func createTier(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createTierRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("id is required"))
			return
		}
		tier, err := svc.CreateTier(services.CreateTierInput{ID: req.ID, CanExpire: req.CanExpire, PriorityOrder: req.PriorityOrder})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, tier)
	}
}

func setTierInactive(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req setInactiveRequest
		_ = c.ShouldBindJSON(&req)
		tier, err := svc.SetTierInactive(c.Param("id"), req.Inactive)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, tier)
	}
}

func deleteTier(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.DeleteTier(c.Param("id")); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type upsertMembershipGradeRequest struct {
	PatronPackageID string  `json:"patronPackageId" binding:"required"`
	PatronTierID    string  `json:"patronTierId" binding:"required"`
	PriceUSD        float64 `json:"priceUsd"`
}

func upsertMembershipGrade(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req upsertMembershipGradeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("patronPackageId and patronTierId are required"))
			return
		}
		grade, err := svc.UpsertMembershipGrade(req.PatronPackageID, req.PatronTierID, req.PriceUSD)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, grade)
	}
}

func deleteMembershipGrade(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("id must be a positive integer"))
			return
		}
		if err := svc.DeleteMembershipGrade(uint(id)); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func setPaymentAssetAllowed(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req setInactiveRequest
		_ = c.ShouldBindJSON(&req)
		asset, err := svc.SetPaymentAssetAllowed(c.Param("symbol"), req.Inactive)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}
