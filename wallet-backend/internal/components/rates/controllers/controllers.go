// Package controllers exposes exchange-rate lookups. The rates component has
// no model of its own - it's a thin HTTP layer over the internal/rates
// Provider interface configured in GlobalConfig.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the rates component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	router.GET("/v1/rates", getRate(gc))
}

func getRate(gc *sharedconfig.GlobalConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		base := c.Query("base")
		quote := c.Query("quote")
		if base == "" || quote == "" {
			apperrors.Abort(c, apperrors.BadRequest("base and quote query parameters are required"))
			return
		}
		rate, err := gc.Rates.GetRate(c.Request.Context(), base, quote)
		if err != nil {
			apperrors.Abort(c, apperrors.NotFound(err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"base": base, "quote": quote, "rate": rate.String()})
	}
}
