package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/swaps/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the swaps component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.Blockchain)

	authed := router.Group("/v1/swaps")
	authed.Use(middleware.StellarSignatureAuth(gc.AuthWindow))
	authed.POST("/build", buildSwap(svc))
	authed.POST("/submit", submitSwap(svc))
}

type buildSwapRequest struct {
	SendCode   string `json:"sendCode"`
	SendIssuer string `json:"sendIssuer"`
	SendAmount string `json:"sendAmount" binding:"required"`
	DestCode   string `json:"destCode"`
	DestIssuer string `json:"destIssuer"`
	DestMin    string `json:"destMin" binding:"required"`
}

func buildSwap(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildSwapRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("sendAmount and destMin are required"))
			return
		}
		publicKey := c.GetString(middleware.CtxPublicKey)
		xdrString, err := svc.BuildSwapXDR(publicKey, req.SendCode, req.SendIssuer, req.SendAmount, req.DestCode, req.DestIssuer, req.DestMin, nil)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"xdr": xdrString})
	}
}

type submitRequest struct {
	SignedXDR string `json:"signedXdr" binding:"required"`
}

func submitSwap(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("signedXdr is required"))
			return
		}
		hash, err := svc.SubmitSwap(req.SignedXDR)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"hash": hash})
	}
}
