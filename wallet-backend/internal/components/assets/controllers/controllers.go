package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/assets/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the assets component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.DB, gc.Blockchain)

	router.GET("/v1/assets", listCurated(svc))

	authed := router.Group("/v1/assets")
	authed.Use(middleware.StellarSignatureAuth(gc.AuthWindow))
	authed.POST("/trustline/build", buildTrustline(svc))
	authed.POST("/trustline/submit", submitTrustline(svc))
}

func listCurated(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assets, err := svc.ListCurated()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, assets)
	}
}

type buildTrustlineRequest struct {
	Code   string `json:"code" binding:"required"`
	Issuer string `json:"issuer" binding:"required"`
	Limit  string `json:"limit"` // optional; "0" removes the trustline, empty means max limit
}

func buildTrustline(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildTrustlineRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("code and issuer are required"))
			return
		}
		publicKey := c.GetString(middleware.CtxPublicKey)
		xdrString, err := svc.BuildTrustlineXDR(publicKey, req.Code, req.Issuer, req.Limit)
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

func submitTrustline(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("signedXdr is required"))
			return
		}
		hash, err := svc.SubmitSignedTransaction(req.SignedXDR)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"hash": hash})
	}
}
