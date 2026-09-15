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
// that build/submit payments on a user's behalf (see servicelinks), and
// its SharedAccess field once sharedaccess's own Service exists (see
// PLAN.md §13.9's flagged follow-up, closed here - payments delegates the
// actual Safe transaction to sharedaccess rather than building one
// itself).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB)

	authed := router.Group("/v1/payments")
	authed.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))
	authed.POST("/build", buildPayment(svc))
	authed.POST("/submit", submitPayment(svc))
	authed.GET("/history/:address", history(svc))

	return svc
}

type buildPaymentRequest struct {
	Destination  string `json:"destination" binding:"required"`
	TokenAddress string `json:"tokenAddress"` // empty = native ETH
	Amount       string `json:"amount" binding:"required"`
	Memo         string `json:"memo"` // optional free-text payment reference, PLAN.md §23
}

// buildPayment proposes the payment as a real Safe transaction on the
// caller's wallet (X-Wallet-Address) and returns the digest the caller's
// own signer key (X-Signer-Address) must personal_sign to approve it -
// see services.PaymentProposal.
func buildPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("destination and amount are required"))
			return
		}
		wallet := c.GetString(middleware.CtxSubject)
		signer := c.GetString(middleware.CtxSigner)
		proposal, err := svc.BuildPaymentTx(c.Request.Context(), wallet, signer, req.Destination, req.TokenAddress, req.Amount, req.Memo)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, proposal)
	}
}

type submitPaymentRequest struct {
	IdempotencyKey string `json:"idempotencyKey" binding:"required"`
	ActionID       uint   `json:"actionId" binding:"required"`
	Signature      string `json:"signature" binding:"required"`
	Destination    string `json:"destination" binding:"required"`
	TokenAddress   string `json:"tokenAddress"`
	Amount         string `json:"amount" binding:"required"`
	Memo           string `json:"memo"` // optional free-text payment reference, PLAN.md §23
}

func submitPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("idempotencyKey, actionId, signature, destination and amount are required"))
			return
		}
		wallet := c.GetString(middleware.CtxSubject)
		signer := c.GetString(middleware.CtxSigner)
		record, err := svc.SubmitPayment(c.Request.Context(), req.IdempotencyKey, req.ActionID, signer, req.Signature, wallet, req.Destination, req.TokenAddress, req.Amount, req.Memo)
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
