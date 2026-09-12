package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/auth/services"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the SIWE auth routes: GET a nonce, POST a signed message to
// exchange it for a session JWT (see internal/components/auth/services for
// the SIWE flow itself).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.Cache, gc.SIWEDomain, gc.ChainID, gc.JWTSecret, gc.JWTExpiry)

	group := router.Group("/v1/auth")
	group.GET("/nonce", getNonce(svc))
	group.POST("/verify", verify(svc))
}

func getNonce(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"nonce": svc.GenerateNonce()})
	}
}

type verifyRequest struct {
	Message   string `json:"message" binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

func verify(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req verifyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("message and signature are required"))
			return
		}
		address, token, err := svc.Verify(req.Message, req.Signature)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"address": address, "token": token})
	}
}
