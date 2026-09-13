package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/services"
)

func registerPurchaseRoutes(authed *gin.RouterGroup, svc *services.Service) {
	authed.POST("/:assetId/subscribe", buildCryptoPurchase(svc))
	authed.POST("/:assetId/subscribe/confirm", confirmCryptoPurchase(svc))
	authed.POST("/:assetId/subscribe/fiat", buildFiatPurchase(svc))
	authed.GET("/:assetId/subscriptions", listSubscriptions(svc))
	authed.GET("/subscriptions/mine", listMySubscriptions(svc))

	authed.POST("/:assetId/interest", expressInterest(svc))
	authed.GET("/:assetId/interest", listInterest(svc))
	authed.GET("/interest/mine", listMyInterest(svc))

	authed.POST("/:assetId/early-exit", buildEarlyExit(svc))
	authed.POST("/:assetId/early-exit/confirm", confirmEarlyExit(svc))
	authed.GET("/early-exits/mine", listMyEarlyExits(svc))
}

type quantityRequest struct {
	Quantity string `json:"quantity" binding:"required"`
}

func buildCryptoPurchase(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req quantityRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity is required"))
			return
		}
		quantity, err := decimal.NewFromString(req.Quantity)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity must be a valid number"))
			return
		}
		tx, paymentAmount, err := svc.BuildCryptoPurchase(c.Request.Context(), userID, assetID, quantity)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"transaction": tx, "paymentAmount": paymentAmount.String()})
	}
}

type confirmCryptoPurchaseRequest struct {
	Quantity string `json:"quantity" binding:"required"`
	TxHash   string `json:"txHash" binding:"required"`
}

func confirmCryptoPurchase(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req confirmCryptoPurchaseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity and txHash are required"))
			return
		}
		quantity, err := decimal.NewFromString(req.Quantity)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity must be a valid number"))
			return
		}
		sub, err := svc.RecordCryptoPurchase(userID, assetID, quantity, req.TxHash)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, sub)
	}
}

type buildFiatPurchaseRequest struct {
	Quantity     string `json:"quantity" binding:"required"`
	Reference    string `json:"reference" binding:"required"`
	FiatCurrency string `json:"fiatCurrency" binding:"required"`
}

func buildFiatPurchase(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req buildFiatPurchaseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity, reference and fiatCurrency are required"))
			return
		}
		quantity, err := decimal.NewFromString(req.Quantity)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity must be a valid number"))
			return
		}
		sub, err := svc.BuildFiatPurchase(c.Request.Context(), userID, assetID, quantity, req.Reference, req.FiatCurrency)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, sub)
	}
}

func listSubscriptions(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		subs, err := svc.ListSubscriptions(assetID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, subs)
	}
}

func listMySubscriptions(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		subs, err := svc.ListMySubscriptions(userID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, subs)
	}
}

type expressInterestRequest struct {
	Amount string `json:"amount"`
}

func expressInterest(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req expressInterestRequest
		_ = c.ShouldBindJSON(&req)
		interest, err := svc.ExpressInterest(userID, assetID, req.Amount)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, interest)
	}
}

func listInterest(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		interests, err := svc.ListExpressionsOfInterest(assetID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, interests)
	}
}

func listMyInterest(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		interests, err := svc.ListMyExpressionsOfInterest(userID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, interests)
	}
}

func buildEarlyExit(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req quantityRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity is required"))
			return
		}
		quantity, err := decimal.NewFromString(req.Quantity)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity must be a valid number"))
			return
		}
		tx, err := svc.BuildEarlyExit(c.Request.Context(), userID, assetID, quantity)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, tx)
	}
}

type confirmEarlyExitRequest struct {
	Quantity      string `json:"quantity" binding:"required"`
	BankID        uint   `json:"bankId" binding:"required"`
	AccountNumber string `json:"accountNumber" binding:"required"`
	AccountName   string `json:"accountName" binding:"required"`
	BurnTxHash    string `json:"burnTxHash" binding:"required"`
}

func confirmEarlyExit(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req confirmEarlyExitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity, bankId, accountNumber, accountName and burnTxHash are required"))
			return
		}
		quantity, err := decimal.NewFromString(req.Quantity)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("quantity must be a valid number"))
			return
		}
		exit, err := svc.RecordEarlyExit(userID, assetID, quantity, req.BankID, req.AccountNumber, req.AccountName, req.BurnTxHash)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, exit)
	}
}

func listMyEarlyExits(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		exits, err := svc.ListMyEarlyExits(userID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, exits)
	}
}
