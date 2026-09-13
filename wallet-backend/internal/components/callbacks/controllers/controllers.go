// Package controllers implements a generic webhook receiver. The upstream
// project this template is derived from wires one handler per external
// provider (1L/OneLiquidity, Doja, Flutterwave) directly into this
// component. This base version ships one provider-agnostic stub instead of
// guessing which vendors a given project will use.
//
// To add a real provider: copy this route, verify the provider's signature
// header (never skip that step - an unverified webhook lets anyone forge
// events), parse its payload shape, and dispatch to the relevant
// component's service (e.g. mark a kyc.Provider verification approved, or
// credit a fiat.Processor charge as settled).
package controllers

import (
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the callbacks component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	router.POST("/v1/callbacks/:provider", receive(gc))
}

func receive(gc *sharedconfig.GlobalConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		provider := c.Param("provider")
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("failed to read request body"))
			return
		}
		// A real integration verifies a provider-specific signature header
		// here before trusting body at all.
		log.Printf("[callbacks:%s] received webhook (%d bytes) - no handler registered", provider, len(body))
		if gc.Alerts != nil {
			_ = gc.Alerts.Notify("unhandled webhook from provider " + provider)
		}
		c.Status(http.StatusOK)
	}
}
