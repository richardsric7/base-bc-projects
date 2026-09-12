// Package controllers implements the root-level endpoints: a health check
// and the SEP-1 stellar.toml well-known file that lets wallets/explorers
// discover which network and assets this service issues.
package controllers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/sharedconfig"
)

// Init registers the root component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	router.GET("/", health(gc))
	router.GET("/.well-known/stellar.toml", stellarToml(gc))
}

func health(gc *sharedconfig.GlobalConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service":           gc.Organisation,
			"status":            "ok",
			"networkPassphrase": gc.NetworkPassphrase,
		})
	}
}

// stellarToml serves a minimal SEP-1 file. Extend ACCOUNTS/CURRENCIES as the
// project issues its own assets. See:
// https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0001.md
func stellarToml(gc *sharedconfig.GlobalConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		toml := fmt.Sprintf("NETWORK_PASSPHRASE=%q\nORG_NAME=%q\n", gc.NetworkPassphrase, gc.Organisation)
		c.String(http.StatusOK, toml)
	}
}
