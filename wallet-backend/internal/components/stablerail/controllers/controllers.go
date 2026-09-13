// Package controllers wires the stablerail component's routes. Every route
// requires a wallet-session JWT; there is no webhook here (unlike
// kyc/fiat) since Stablerail's API is entirely poll-driven from this
// backend's side - see services.PollPendingOnboarding/PollPendingOnramp,
// run from background goroutines in main.go.
package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/stablerail/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the stablerail component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.StablerailAPIKey, gc.StablerailBaseURL, gc.StablerailEnabled)

	authed := router.Group("/v1/stablerail")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.GET("/banks", listBanks(svc))
	authed.POST("/onboard/:bvn", initiateOnboarding(svc))
	authed.POST("/onramp/:amount", initiateOnramp(svc))

	return svc
}

func listBanks(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		banks, err := svc.ListBanks()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, banks)
	}
}

func initiateOnboarding(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		bvn := c.Param("bvn")
		if len(bvn) != 11 {
			apperrors.Abort(c, apperrors.BadRequest("BVN must be 11 digits"))
			return
		}
		message, err := svc.InitiateOnboarding(c.GetString(middleware.CtxSubject), bvn)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": message})
	}
}

func initiateOnramp(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		amount, err := strconv.ParseFloat(c.Param("amount"), 64)
		if err != nil || amount <= 0 {
			apperrors.Abort(c, apperrors.BadRequest("invalid amount"))
			return
		}
		result, err := svc.InitiateOnramp(c.GetString(middleware.CtxSubject), amount)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
