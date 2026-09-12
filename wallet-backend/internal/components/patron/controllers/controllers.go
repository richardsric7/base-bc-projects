// Package controllers wires HTTP routes to the patron/membership
// services. See PLAN.md §4.10.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/patron/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the patron component's routes on router and returns the
// underlying Service so main.go can run its background activation worker.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.Blockchain, gc.Rates, gc.PatronFeeWalletSalt, gc.PatronVATPercent)

	router.GET("/v1/patron/reference", getReferenceData(svc))

	authed := router.Group("/v1/patron")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.GET("", getPatronStatus(svc))
	authed.GET("/history", getSubscriptionHistory(svc))
	authed.POST("/subscribe", buildSubscription(svc))
	authed.POST("/subscribe/confirm", confirmSubscription(svc))

	return svc
}

func resolveUserID(c *gin.Context, svc *services.Service) (uint, bool) {
	userID, err := svc.ResolveUserID(c.GetString(middleware.CtxSubject))
	if err != nil {
		apperrors.AbortAny(c, err)
		return 0, false
	}
	return userID, true
}

func getReferenceData(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := svc.ReferenceData()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, data)
	}
}

func getPatronStatus(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		membership, err := svc.GetMembership(userID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"membership": membership})
	}
}

func getSubscriptionHistory(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		logs, err := svc.GetSubscriptionLogs(userID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, logs)
	}
}

type subscribeRequest struct {
	MembershipGradeID  uint   `json:"membershipGradeId" binding:"required"`
	PaymentAssetSymbol string `json:"paymentAssetSymbol" binding:"required"`
}

func buildSubscription(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		var req subscribeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("membershipGradeId and paymentAssetSymbol are required"))
			return
		}
		tx, quote, err := svc.BuildSubscription(c.Request.Context(), userID, req.MembershipGradeID, req.PaymentAssetSymbol)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"transaction": tx, "quote": quote})
	}
}

type confirmSubscribeRequest struct {
	MembershipGradeID  uint   `json:"membershipGradeId" binding:"required"`
	PaymentAssetSymbol string `json:"paymentAssetSymbol" binding:"required"`
	TxHash             string `json:"txHash" binding:"required"`
}

func confirmSubscription(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		var req confirmSubscribeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("membershipGradeId, paymentAssetSymbol and txHash are required"))
			return
		}
		log, err := svc.ConfirmSubscription(c.Request.Context(), userID, req.MembershipGradeID, req.PaymentAssetSymbol, req.TxHash)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, log)
	}
}
