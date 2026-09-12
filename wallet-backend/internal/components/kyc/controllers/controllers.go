// Package controllers wires the kyc component's routes. The
// verification-flow routes (levels/progress/initiate) require a
// wallet-session JWT like the rest of the API; the two webhook routes are
// deliberately unauthenticated HTTP endpoints (a vendor calls them, not a
// logged-in user) that authenticate the request itself via a
// vendor-specific signature header - never trust either payload without
// that check passing first.
package controllers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/kyc/models"
	"wallet-backend/internal/components/kyc/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the kyc component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.DB, gc.SumsubBaseURL, gc.SumsubToken, gc.SumsubSecretKey, gc.DojaSecretKey)

	authed := router.Group("/v1/kyc")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.GET("/sumsub/levels", listSumsubLevels(svc))
	authed.GET("/sumsub/progress", getSumsubProgress(svc))
	authed.POST("/sumsub/initiate/:levelName", initiateSumsubLevel(svc))
	authed.GET("/doja/widgets", listDojaWidgets(svc))
	authed.GET("/doja/progress", getDojaProgress(svc))

	webhooks := router.Group("/v1/callbacks/kyc")
	webhooks.POST("/sumsub/webhook", sumsubWebhook(svc))
	webhooks.POST("/doja/webhook", dojaWebhook(svc))
}

func listSumsubLevels(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		levels, err := svc.ListSumsubLevels()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, levels)
	}
}

func getSumsubProgress(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		progress, err := svc.GetSumsubProgress(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, progress)
	}
}

func initiateSumsubLevel(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		address := c.GetString(middleware.CtxSubject)
		token, applicant, err := svc.InitiateSumsubLevel(address, c.Param("levelName"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"applicantToken": token, "applicant": applicant})
	}
}

func listDojaWidgets(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		widgets, err := svc.ListDojaWidgets()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, widgets)
	}
}

func getDojaProgress(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		progress, err := svc.GetDojaProgress(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, progress)
	}
}

// sumsubWebhook verifies Sumsub's HMAC-SHA256 digest header before trusting
// the payload at all - see Service.VerifySumsubWebhookSignature's doc
// comment for the bug this fixes relative to the original.
func sumsubWebhook(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("failed to read request body"))
			return
		}
		digest := c.GetHeader("x-payload-digest")
		alg := c.GetHeader("x-payload-digest-alg")
		if !svc.VerifySumsubWebhookSignature(body, digest, alg) {
			apperrors.Abort(c, apperrors.Unauthorized("invalid webhook signature"))
			return
		}

		var input models.SumsubWebhookInput
		if err := json.Unmarshal(body, &input); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("invalid webhook payload"))
			return
		}
		if err := svc.ProcessSumsubWebhook(&input); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, "success")
	}
}

// dojaWebhook verifies Dojah's HMAC-SHA256 signature header before trusting
// the payload at all - see Service.VerifyDojaWebhookSignature's doc comment
// for the bug this fixes relative to the original.
func dojaWebhook(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("failed to read request body"))
			return
		}
		signature := c.GetHeader("x-dojah-signature")
		if !svc.VerifyDojaWebhookSignature(body, signature) {
			apperrors.Abort(c, apperrors.Unauthorized("invalid webhook signature"))
			return
		}

		var event models.DojaWebhookEvent
		if err := json.Unmarshal(body, &event); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("invalid webhook payload"))
			return
		}
		if err := svc.ProcessDojaWebhook(&event); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, "success")
	}
}
