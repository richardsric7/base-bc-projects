// Package controllers wires HTTP routes to the reference-data services:
// a public read surface (country catalog/config, active forms) and an
// admin surface to maintain them. See PLAN.md §4.13.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/reference/models"
	"wallet-backend/internal/components/reference/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the reference component's routes on router and returns
// the underlying Service so main.go can wire it into the registration
// risk-flagging hook (see internal/geoip, PLAN.md §4.13).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB)

	public := router.Group("/v1/reference")
	public.GET("/countries", listCountries(svc))
	public.GET("/countries/:code/config", getCountryConfig(svc))
	public.GET("/forms", listForms(svc))
	public.GET("/forms/:id", getForm(svc))

	admin := router.Group("/v1/admin/reference")
	admin.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceAdmin))
	admin.PUT("/countries/:code", upsertCountry(svc))
	admin.PUT("/countries/:code/config", upsertCountryConfig(svc))
	admin.PUT("/forms/:id", upsertForm(svc))
	admin.DELETE("/forms/:id", deactivateForm(svc))

	return svc
}

func listCountries(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		countries, err := svc.ListCountries()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, countries)
	}
}

func getCountryConfig(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := svc.GetCountryConfig(c.Param("code"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		if config == nil {
			apperrors.Abort(c, apperrors.NotFound("no config for this country - a global default applies"))
			return
		}
		c.JSON(http.StatusOK, config)
	}
}

type upsertCountryRequest struct {
	Name    string `json:"name" binding:"required"`
	Enabled bool   `json:"enabled"`
}

func upsertCountry(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req upsertCountryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("name is required"))
			return
		}
		country, err := svc.UpsertCountry(c.Param("code"), req.Name, req.Enabled)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, country)
	}
}

type upsertCountryConfigRequest struct {
	FiatCurrency         string  `json:"fiatCurrency"`
	FiatActivationAmount float64 `json:"fiatActivationAmount"`
	RewardTokenPercent   float64 `json:"rewardTokenPercent"`
	RegulatorName        string  `json:"regulatorName"`
	HighRisk             bool    `json:"highRisk"`
}

func upsertCountryConfig(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req upsertCountryConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("invalid request body"))
			return
		}
		config, err := svc.UpsertCountryConfig(models.CountryConfig{
			CountryCode:          c.Param("code"),
			FiatCurrency:         req.FiatCurrency,
			FiatActivationAmount: req.FiatActivationAmount,
			RewardTokenPercent:   req.RewardTokenPercent,
			RegulatorName:        req.RegulatorName,
			HighRisk:             req.HighRisk,
		})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, config)
	}
}

func listForms(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		forms, err := svc.ListActiveForms()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, forms)
	}
}

func getForm(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		form, err := svc.GetForm(c.Param("id"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, form)
	}
}

type upsertFormRequest struct {
	Name   string `json:"name" binding:"required"`
	Schema string `json:"schema" binding:"required"`
}

func upsertForm(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req upsertFormRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("name and schema are required"))
			return
		}
		form, err := svc.UpsertForm(c.Param("id"), req.Name, req.Schema)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, form)
	}
}

func deactivateForm(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.DeactivateForm(c.Param("id")); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
