package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/services"
)

// registerUserRoutes registers onboarding, KYC-override, user-info lookup,
// push relay and token-symbol lookup - each gated by its own granular
// capability (PLAN.md §4.11 finding 4) and, where the route names a
// specific user, scoped by requireOwnedUser inside the service layer
// (finding 5).
func registerUserRoutes(partner *gin.RouterGroup, svc *services.Service) {
	partner.POST("/users", onboardUser(svc))
	partner.GET("/users/:userId", getUserInfo(svc))
	partner.PUT("/users/:userId/kyc", updateKYCStatus(svc))
	partner.POST("/users/:userId/push", sendPush(svc))
	partner.GET("/tokens/:symbol", getTokenInfo(svc))
}

type onboardUserRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required"`
	Address  string `json:"address" binding:"required"`
}

func onboardUser(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanCreateUsers) {
			return
		}
		var req onboardUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("username, email and address are required"))
			return
		}
		user, err := svc.OnboardUser(link.ID, req.Username, req.Email, req.Address)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusCreated, user)
	}
}

func getUserInfo(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanViewUserInfo) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		user, err := svc.UserInfo(link.ID, userID)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

type updateKYCStatusRequest struct {
	Level int `json:"level"`
}

func updateKYCStatus(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanUpdateKYC) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		var req updateKYCStatusRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("level is required"))
			return
		}
		if err := svc.UpdateKYCStatus(link.ID, userID, req.Level); err != nil {
			writeError(c, err)
			return
		}
		noContent(c)
	}
}

type sendPushRequest struct {
	DeviceToken string `json:"deviceToken" binding:"required"`
	Title       string `json:"title" binding:"required"`
	Body        string `json:"body" binding:"required"`
}

func sendPush(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanSendPushNotifications) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		var req sendPushRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("deviceToken, title and body are required"))
			return
		}
		if err := svc.SendPush(link.ID, userID, req.DeviceToken, req.Title, req.Body); err != nil {
			writeError(c, err)
			return
		}
		noContent(c)
	}
}

func getTokenInfo(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanLookupTokenInfo) {
			return
		}
		token, err := svc.TokenInfo(c.Param("symbol"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, token)
	}
}
