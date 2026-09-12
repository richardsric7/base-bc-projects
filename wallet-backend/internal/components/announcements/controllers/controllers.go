// Package controllers wires the announcements routes. This component is the
// template's example of the JWT admin-auth scheme: the public GET is open to
// everyone, the POST is gated by middleware.JWTAuth.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/announcements/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the announcements component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.DB)

	router.GET("/v1/announcements", listAnnouncements(svc))

	admin := router.Group("/v1/admin/announcements")
	admin.Use(middleware.JWTAuth(gc.JWTSecret))
	admin.POST("", createAnnouncement(svc))
}

func listAnnouncements(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		announcements, err := svc.ListActive()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, announcements)
	}
}

type createAnnouncementRequest struct {
	Title string `json:"title" binding:"required"`
	Body  string `json:"body" binding:"required"`
}

func createAnnouncement(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createAnnouncementRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("title and body are required"))
			return
		}
		announcement, err := svc.Create(req.Title, req.Body)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, announcement)
	}
}
