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
// (see Service.Alerts's doc comment, PLAN.md §4.13).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.Blockchain)

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
	Nonce         *uint64       `json:"nonce"`
}

func buildSwap(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildSwapRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("routerAddress, routerAbi and method are required"))
			return
		}
		tx, err := svc.BuildSwapTx(c.Request.Context(), services.BuildSwapInput{
			From:          c.GetString(middleware.CtxSubject),
			RouterAddress: req.RouterAddress,
			RouterABI:     req.RouterABI,
			Method:        req.Method,
			Args:          req.Args,
			ValueWei:      req.ValueWei,
			Nonce:         req.Nonce,
		})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, tx)
	}
}

type submitRequest struct {
	SignedTx string `json:"signedTx" binding:"required"`
}

func submitSwap(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("signedTx is required"))
			return
		}
		hash, err := svc.SubmitSwap(c.Request.Context(), req.SignedTx)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"hash": hash})
	}
}
