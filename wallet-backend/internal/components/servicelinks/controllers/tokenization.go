package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/services"
)

// registerTokenizationRoutes registers the CanManageTokenization-gated
// passthrough onto the tokenization component for a service link's own
// onboarded users - see services/tokenization.go's package doc comment.
func registerTokenizationRoutes(partner *gin.RouterGroup, svc *services.Service) {
	partner.GET("/tokenization/:assetId", getAssetInfo(svc))
	partner.POST("/users/:userId/tokenization/:assetId/purchase/build", buildPartnerTokenPurchase(svc))
	partner.POST("/users/:userId/tokenization/:assetId/purchase/record", recordPartnerTokenPurchase(svc))
	partner.GET("/users/:userId/tokenization/subscriptions", getPartnerTokenSubscriptions(svc))
}

func getAssetInfo(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanManageTokenization) {
			return
		}
		assetID, ok := uintParam(c, "assetId")
		if !ok {
			return
		}
		asset, err := svc.AssetInfo(assetID)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

type partnerTokenPurchaseRequest struct {
	Quantity string `json:"quantity" binding:"required"`
}

func buildPartnerTokenPurchase(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanManageTokenization) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		assetID, ok := uintParam(c, "assetId")
		if !ok {
			return
		}
		var req partnerTokenPurchaseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity is required"))
			return
		}
		quantity, err := decimal.NewFromString(req.Quantity)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity must be a decimal number"))
			return
		}
		tx, cost, err := svc.BuildPartnerTokenPurchase(c.Request.Context(), link.ID, userID, assetID, quantity)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"transaction": tx, "cost": cost.String()})
	}
}

type recordPartnerTokenPurchaseRequest struct {
	Quantity string `json:"quantity" binding:"required"`
	TxHash   string `json:"txHash" binding:"required"`
}

func recordPartnerTokenPurchase(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanManageTokenization) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		assetID, ok := uintParam(c, "assetId")
		if !ok {
			return
		}
		var req recordPartnerTokenPurchaseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity and txHash are required"))
			return
		}
		quantity, err := decimal.NewFromString(req.Quantity)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity must be a decimal number"))
			return
		}
		subscription, err := svc.RecordPartnerTokenPurchase(link.ID, userID, assetID, quantity, req.TxHash)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, subscription)
	}
}

func getPartnerTokenSubscriptions(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanManageTokenization) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		subscriptions, err := svc.PartnerTokenSubscriptions(link.ID, userID)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, subscriptions)
	}
}
