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
}

type buildPartnerPaymentRequest struct {
	To           string  `json:"to" binding:"required"`
	TokenAddress string  `json:"tokenAddress"`
	Amount       string  `json:"amount" binding:"required"`
	Nonce        *uint64 `json:"nonce"`
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
		tx, err := svc.BuildPartnerPayment(c.Request.Context(), link.ID, userID, req.To, req.TokenAddress, req.Amount, req.Nonce)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, tx)
	}
}

type submitPartnerPaymentRequest struct {
	IdempotencyKey string `json:"idempotencyKey" binding:"required"`
	SignedTx       string `json:"signedTx" binding:"required"`
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
			apperrors.Abort(c, apperrors.BadRequest("idempotencyKey, signedTx, to and amount are required"))
			return
		}
		record, err := svc.SubmitPartnerPayment(c.Request.Context(), link.ID, userID, req.IdempotencyKey, req.SignedTx, req.To, req.TokenAddress, req.Amount)
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
