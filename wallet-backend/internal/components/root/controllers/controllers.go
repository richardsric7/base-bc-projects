// Package controllers implements the root-level endpoints: a health check
// reporting which chain this service is configured for. Stellar's SEP-1
// stellar.toml has no EVM equivalent - token discovery on Base is just
// reading a contract, not fetching a well-known file - so this component
// drops that endpoint entirely rather than replacing it with a no-op.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/sharedconfig"
)

// Init registers the root component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	router.GET("/", health(gc))
}

func health(gc *sharedconfig.GlobalConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": gc.Organisation,
			"status":  "ok",
			"chainId": gc.ChainID,
		})
	}
}
