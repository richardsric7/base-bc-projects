// Package controllers wires the crypto component's routes. Every route
// requires a signed request (middleware.SignatureAuth); there is no webhook here (like
// stablerail) since deposit discovery is poll-driven from this backend's
// side - see services.PollNewDeposits, run from a background goroutine in
// main.go.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/crypto/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the crypto component's routes on router and returns the
// underlying Service so main.go can run its background deposit poller.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.Blockchain, gc.OneLiquidityBaseURL, gc.OneLiquidityToken, gc.CryptoWalletDomain, gc.CryptoTreasuryKeySalt, gc.CryptoWithdrawalServiceFeePercent)

	authed := router.Group("/v1/crypto")
	authed.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))
	authed.GET("/deposit-address/:currency", getDepositAddresses(svc))
	authed.GET("/deposit-history", getDepositHistory(svc))
	authed.GET("/withdrawal-networks/:currency", getWithdrawalNetworks(svc))
	authed.POST("/withdrawals/build", buildWithdrawal(svc))
	authed.POST("/withdrawals/confirm", confirmWithdrawal(svc))
	authed.GET("/withdrawal-history", getWithdrawalHistory(svc))

	return svc
}

func getDepositAddresses(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		addresses, err := svc.EnsureDepositAddresses(c.GetString(middleware.CtxSubject), c.Param("currency"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, addresses)
	}
}

func getDepositHistory(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		deposits, err := svc.ListDeposits(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, deposits)
	}
}

func getWithdrawalNetworks(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := svc.GetWithdrawalNetworks(c.Param("currency"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

type buildWithdrawalRequest struct {
	Currency string  `json:"currency" binding:"required"`
	Network  string  `json:"network" binding:"required"`
	Amount   float64 `json:"amount" binding:"required"`
}

func buildWithdrawal(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildWithdrawalRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("currency, network and amount are required"))
			return
		}
		wallet := c.GetString(middleware.CtxSubject)
		signer := c.GetString(middleware.CtxSigner)
		proposal, err := svc.BuildWithdrawal(c.Request.Context(), wallet, req.Currency, req.Network, req.Amount, signer)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, proposal)
	}
}

type confirmWithdrawalRequest struct {
	Currency  string  `json:"currency" binding:"required"`
	Network   string  `json:"network" binding:"required"`
	ToAddress string  `json:"toAddress" binding:"required"`
	Amount    float64 `json:"amount" binding:"required"`
	ActionID  uint    `json:"actionId" binding:"required"`
	Signature string  `json:"signature" binding:"required"`
}

func confirmWithdrawal(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req confirmWithdrawalRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("currency, network, toAddress, amount, actionId and signature are required"))
			return
		}
		wallet := c.GetString(middleware.CtxSubject)
		signer := c.GetString(middleware.CtxSigner)
		result, err := svc.ConfirmWithdrawal(c.Request.Context(), wallet, req.Currency, req.Network, req.ToAddress, req.Amount, req.ActionID, signer, req.Signature)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, result)
	}
}

func getWithdrawalHistory(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		withdrawals, err := svc.ListWithdrawals(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, withdrawals)
	}
}
