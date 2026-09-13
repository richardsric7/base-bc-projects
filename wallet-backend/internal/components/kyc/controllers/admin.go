package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/kyc/services"
)

// registerAdminRoutes registers KYC-config management - maintaining the
// Sumsub level catalog and Doja widget-ID mapping an operator needs to
// keep in sync with their vendor dashboards (see models.SumsubLevel/
// DojaWidget's doc comments). Gated by middleware.AudienceAdmin, the same
// admin-JWT mechanism established for announcements/tokenization/
// servicelinks - see PLAN.md §4.12.
func registerAdminRoutes(admin *gin.RouterGroup, svc *services.Service) {
	admin.POST("/sumsub/levels", createSumsubLevel(svc))
	admin.PUT("/sumsub/levels/:id", updateSumsubLevel(svc))
	admin.DELETE("/sumsub/levels/:id", deleteSumsubLevel(svc))
	admin.POST("/doja/widgets", createDojaWidget(svc))
	admin.PUT("/doja/widgets/:id", updateDojaWidget(svc))
	admin.DELETE("/doja/widgets/:id", deleteDojaWidget(svc))
}

type sumsubLevelRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func createSumsubLevel(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req sumsubLevelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("name is required"))
			return
		}
		level, err := svc.CreateSumsubLevel(services.CreateSumsubLevelInput{Name: req.Name, Description: req.Description})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, level)
	}
}

func updateSumsubLevel(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseUintParam(c, "id")
		if !ok {
			return
		}
		var req sumsubLevelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("description is required"))
			return
		}
		level, err := svc.UpdateSumsubLevel(id, req.Description)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, level)
	}
}

func deleteSumsubLevel(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseUintParam(c, "id")
		if !ok {
			return
		}
		if err := svc.DeleteSumsubLevel(id); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type dojaWidgetRequest struct {
	ID        string `json:"id"`
	Level     int    `json:"level"`
	Corporate bool   `json:"corporate"`
}

func createDojaWidget(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req dojaWidgetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("id is required"))
			return
		}
		widget, err := svc.CreateDojaWidget(services.CreateDojaWidgetInput{ID: req.ID, Level: req.Level, Corporate: req.Corporate})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, widget)
	}
}

func updateDojaWidget(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req dojaWidgetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("level is required"))
			return
		}
		widget, err := svc.UpdateDojaWidget(c.Param("id"), req.Level, req.Corporate)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, widget)
	}
}

func deleteDojaWidget(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.DeleteDojaWidget(c.Param("id")); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func parseUintParam(c *gin.Context, name string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil {
		apperrors.Abort(c, apperrors.BadRequest(name+" must be a positive integer"))
		return 0, false
	}
	return uint(value), true
}
