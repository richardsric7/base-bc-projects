package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/assets/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the assets component's routes on router and returns the
// underlying Service so main.go can wire it into other components that
// need curated-token/balance lookups (see servicelinks).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.Blockchain)

	router.GET("/v1/assets", listCurated(svc))
	router.GET("/v1/assets/balance/:address", getBalance(svc))

	authed := router.Group("/v1/assets")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.POST("/approve/build", buildApprove(svc))
	authed.POST("/approve/submit", submit(svc))

	// Admin: balance lookup for any address, e.g. support/investigation
	// tooling - reuses the same Service.Balance the public/authed routes
	// above use, just without a per-caller address restriction. Gated by
	// middleware.AudienceAdmin, same as every other admin surface in this
	// port - see PLAN.md §4.12.
	admin := router.Group("/v1/admin/wallet")
	admin.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceAdmin))
	admin.GET("/:address/balance", getBalance(svc))

	return svc
}

func listCurated(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokens, err := svc.ListCurated()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, tokens)
	}
}

func getBalance(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenAddress := c.Query("token") // empty = native ETH balance
		balance, err := svc.Balance(c.Request.Context(), c.Param("address"), tokenAddress)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"balance": balance})
	}
}

type buildApproveRequest struct {
	TokenAddress string  `json:"tokenAddress" binding:"required"`
	Spender      string  `json:"spender" binding:"required"`
	Amount       string  `json:"amount" binding:"required"`
	Nonce        *uint64 `json:"nonce"` // optional - see PLAN.md §3 on offline-batched nonces
}

func buildApprove(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req buildApproveRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("tokenAddress, spender and amount are required"))
			return
		}
		owner := c.GetString(middleware.CtxSubject)
		tx, err := svc.BuildApproveTx(c.Request.Context(), owner, req.TokenAddress, req.Spender, req.Amount, req.Nonce)
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

func submit(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req submitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("signedTx is required"))
			return
		}
		hash, err := svc.SubmitSignedTransaction(c.Request.Context(), req.SignedTx)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"hash": hash})
	}
}
