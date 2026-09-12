package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/payments/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the payments component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.DB, gc.Blockchain)

	authed := router.Group("/v1/payments")
	authed.Use(middleware.StellarSignatureAuth(gc.AuthWindow))
	authed.POST("/build", buildPayment(svc))
	authed.POST("/submit", submitPayment(svc))
	authed.GET("/history/:publicKey", history(svc))
}

type buildPaymentRequest struct {
	Destination string `json:"destination" binding:"required"`
	AssetCode   string `json:"assetCode"`
	AssetIssuer string `json:"assetIssuer"`
	Amount      string `json:"amount" binding:"required"`
}

func buildPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("destination and amount are required"))
			return
		}
		source := c.GetString(middleware.CtxPublicKey)
		xdrString, err := svc.BuildPaymentXDR(source, req.Destination, req.AssetCode, req.AssetIssuer, req.Amount)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"xdr": xdrString})
	}
}

type submitPaymentRequest struct {
	SignedXDR   string `json:"signedXdr" binding:"required"`
	Destination string `json:"destination" binding:"required"`
	AssetCode   string `json:"assetCode"`
	AssetIssuer string `json:"assetIssuer"`
	Amount      string `json:"amount" binding:"required"`
}

func submitPayment(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitPaymentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("signedXdr, destination and amount are required"))
			return
		}
		source := c.GetString(middleware.CtxPublicKey)
		record, err := svc.SubmitPayment(req.SignedXDR, source, req.Destination, req.AssetCode, req.AssetIssuer, req.Amount)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, record)
	}
}

func history(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		records, err := svc.History(c.Param("publicKey"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, records)
	}
}
