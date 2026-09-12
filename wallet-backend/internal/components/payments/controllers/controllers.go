package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/payments/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the payments component's routes on router and returns
// the underlying Service so main.go can wire it into other components
// that build/submit payments on a user's behalf (see servicelinks).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.Blockchain)

	authed := router.Group("/v1/payments")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.POST("/build", buildPayment(svc))
	authed.POST("/submit", submitPayment(svc))
	authed.GET("/history/:address", history(svc))

	return svc
}

type buildPaymentRequest struct {
	Destination  string  `json:"destination" binding:"required"`
	TokenAddress string  `json:"tokenAddress"` // empty = native ETH
	Amount       string  `json:"amount" binding:"required"`
	Nonce        *uint64 `json:"nonce"` // optional - see PLAN.md §3 on offline-batched nonces
}

func buildPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("destination and amount are required"))
			return
		}
		source := c.GetString(middleware.CtxSubject)
		tx, err := svc.BuildPaymentTx(c.Request.Context(), source, req.Destination, req.TokenAddress, req.Amount, req.Nonce)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, tx)
	}
}

type submitPaymentRequest struct {
	IdempotencyKey string `json:"idempotencyKey" binding:"required"`
	SignedTx       string `json:"signedTx" binding:"required"`
	Destination    string `json:"destination" binding:"required"`
	TokenAddress   string `json:"tokenAddress"`
	Amount         string `json:"amount" binding:"required"`
}

func submitPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("idempotencyKey, signedTx, destination and amount are required"))
			return
		}
		source := c.GetString(middleware.CtxSubject)
		record, err := svc.SubmitPayment(c.Request.Context(), req.IdempotencyKey, req.SignedTx, source, req.Destination, req.TokenAddress, req.Amount)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, record)
	}
}

func history(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		records, err := svc.History(c.Param("address"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, records)
	}
}
