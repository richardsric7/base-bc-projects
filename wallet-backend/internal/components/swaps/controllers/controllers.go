package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/swaps/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the swaps component's routes on router and returns the
// underlying Service so main.go can wire in the real alerting.Notifier
// (see Service.Alerts's doc comment, PLAN.md §4.13) and its SharedAccess
// field (PLAN.md §13.9's flagged follow-up, closed here).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New()

	authed := router.Group("/v1/swaps")
	authed.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))
	authed.POST("/build", buildSwap(svc))
	authed.POST("/submit", submitSwap(svc))

	return svc
}

type buildSwapRequest struct {
	RouterAddress string        `json:"routerAddress" binding:"required"`
	RouterABI     string        `json:"routerAbi" binding:"required"`
	Method        string        `json:"method" binding:"required"`
	Args          []interface{} `json:"args"`
	ValueWei      string        `json:"valueWei"`
}

func buildSwap(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildSwapRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("routerAddress, routerAbi and method are required"))
			return
		}
		proposal, err := svc.BuildSwapTx(c.Request.Context(), services.BuildSwapInput{
			WalletAddress: c.GetString(middleware.CtxSubject),
			SignerAddress: c.GetString(middleware.CtxSigner),
			RouterAddress: req.RouterAddress,
			RouterABI:     req.RouterABI,
			Method:        req.Method,
			Args:          req.Args,
			ValueWei:      req.ValueWei,
		})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, proposal)
	}
}

type submitRequest struct {
	ActionID  uint   `json:"actionId" binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

func submitSwap(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("actionId and signature are required"))
			return
		}
		signer := c.GetString(middleware.CtxSigner)
		hash, err := svc.SubmitSwap(c.Request.Context(), req.ActionID, signer, req.Signature)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"hash": hash})
	}
}
