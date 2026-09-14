package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/services"
)

// registerPaymentRoutes registers the partner payment/balance surface for
// a service link's own onboarded users. Every route requires
// CanSendPayments or CanReadBalances and, via requireOwnedUser inside the
// service layer, that the named user was actually created by the calling
// service link (PLAN.md §4.11 finding 5 - the original trusted unverified
// request headers here instead of checking wallet ownership at all).
func registerPaymentRoutes(partner *gin.RouterGroup, svc *services.Service) {
	partner.POST("/users/:userId/payments/build", buildPartnerPayment(svc))
	partner.POST("/users/:userId/payments/submit", submitPartnerPayment(svc))
	partner.GET("/users/:userId/payments", getPaymentHistory(svc))
	partner.GET("/users/:userId/balance", getBalance(svc))
	partner.POST("/users/:userId/wallets", registerSubWallet(svc))
	partner.GET("/users/:userId/payment-request", requestPartnerPaymentLink(svc))
}

// requestPartnerPaymentLink mints a "receive payment" QR/deep-link
// (PLAN.md §14.1a/§14.2 item 1) - the partner-side half of the
// payment-request-link pair, mirroring the original's query-param shape
// (to/tokenAddress/amount/memo replacing paymentDestination/assetCode/
// assetIssuer/amount/memo) but scoped through requireOwnedUser like
// every other route in this file, tighter than the original's own
// ownership-blind version of this specific route.
func requestPartnerPaymentLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanSendPayments) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		result, err := svc.RequestPaymentLinkForOwnedUser(link.ID, userID, c.Query("to"), c.Query("tokenAddress"), c.Query("amount"), c.Query("memo"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

type buildPartnerPaymentRequest struct {
	To           string `json:"to" binding:"required"`
	TokenAddress string `json:"tokenAddress"`
	Amount       string `json:"amount" binding:"required"`
}

func buildPartnerPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanSendPayments) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		var req buildPartnerPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("to and amount are required"))
			return
		}
		proposal, err := svc.BuildPartnerPayment(c.Request.Context(), link.ID, userID, req.To, req.TokenAddress, req.Amount)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, proposal)
	}
}

type submitPartnerPaymentRequest struct {
	IdempotencyKey string `json:"idempotencyKey" binding:"required"`
	ActionID       uint   `json:"actionId" binding:"required"`
	Signature      string `json:"signature" binding:"required"`
	To             string `json:"to" binding:"required"`
	TokenAddress   string `json:"tokenAddress"`
	Amount         string `json:"amount" binding:"required"`
}

func submitPartnerPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanSendPayments) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		var req submitPartnerPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("idempotencyKey, actionId, signature, to and amount are required"))
			return
		}
		record, err := svc.SubmitPartnerPayment(c.Request.Context(), link.ID, userID, req.IdempotencyKey, req.ActionID, req.Signature, req.To, req.TokenAddress, req.Amount)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, record)
	}
}

func getPaymentHistory(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanReadBalances) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		history, err := svc.PaymentHistory(link.ID, userID)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, history)
	}
}

func getBalance(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanReadBalances) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		balance, err := svc.Balance(c.Request.Context(), link.ID, userID, c.Query("token"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"balance": balance})
	}
}

type registerSubWalletRequest struct {
	Address string `json:"address" binding:"required"`
	Label   string `json:"label"`
}

func registerSubWallet(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if !requireCapability(c, link.CanSendPayments) {
			return
		}
		userID, ok := uintParam(c, "userId")
		if !ok {
			return
		}
		var req registerSubWalletRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("address is required"))
			return
		}
		wallet, err := svc.RegisterSubWallet(link.ID, userID, req.Address, req.Label)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusCreated, wallet)
	}
}
