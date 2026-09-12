// Package controllers wires the market component's routes. Every route
// requires a wallet-session JWT except the public order-book/trade-history
// views, which any caller (even unauthenticated) can read.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/market/models"
	"wallet-backend/internal/components/market/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the market component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.DB, gc.Blockchain, gc.MarketEscrowKeySalt)

	public := router.Group("/v1/market")
	public.GET("/escrow-address", getEscrowAddress(svc))
	public.GET("/orderbook", getOrderBook(svc))
	public.GET("/trades", getTrades(svc))

	authed := router.Group("/v1/market")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.POST("/offers", createOffer(svc))
	authed.GET("/offers", listMyOffers(svc))
	authed.DELETE("/offers/:offerId", cancelOffer(svc))
}

func getEscrowAddress(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		address, err := svc.EscrowAddress()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"escrowAddress": address})
	}
}

func getOrderBook(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		baseToken := c.Query("baseToken")
		quoteToken := c.Query("quoteToken")
		buys, sells, err := svc.ListOrderBook(baseToken, quoteToken)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"buys": buys, "sells": sells})
	}
}

func getTrades(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		trades, err := svc.ListTrades(c.Query("baseToken"), c.Query("quoteToken"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, trades)
	}
}

type createOfferRequest struct {
	OfferType    string `json:"offerType" binding:"required"`
	BaseToken    string `json:"baseToken" binding:"required"`
	QuoteToken   string `json:"quoteToken" binding:"required"`
	PricePerUnit string `json:"pricePerUnit" binding:"required"`
	Quantity     string `json:"quantity" binding:"required"`
}

func createOffer(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createOfferRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("offerType, baseToken, quoteToken, pricePerUnit and quantity are required"))
			return
		}
		offer, err := svc.PlaceOffer(c.GetString(middleware.CtxSubject), models.OfferType(req.OfferType), req.BaseToken, req.QuoteToken, req.PricePerUnit, req.Quantity)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, offer)
	}
}

func listMyOffers(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		offers, err := svc.ListMyOffers(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, offers)
	}
}

func cancelOffer(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		offer, err := svc.CancelOffer(c.GetString(middleware.CtxSubject), c.Param("offerId"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, offer)
	}
}
