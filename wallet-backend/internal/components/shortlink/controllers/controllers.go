// Package controllers wires HTTP routes to the shortlink services: create
// a link, redirect through it, and fetch its QR code. See PLAN.md §4.13.
package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/shortlink/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the shortlink component's routes on router and returns
// the underlying Service so other components can mint links directly
// (e.g. a referral link, or a servicelinks approval deep link) without a
// second HTTP round-trip to their own API.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.ShortlinkBaseURL)

	// The redirect and QR routes are deliberately public and unauthenticated -
	// that's the entire point of a shareable short link or a QR code someone
	// scans from a printed flyer.
	router.GET("/s/:code", redirect(svc))
	router.GET("/s/:code/qr", getQRCode(svc))

	authed := router.Group("/v1/shortlinks")
	authed.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))
	authed.POST("", createLink(svc))

	return svc
}

type createLinkRequest struct {
	TargetURL string `json:"targetUrl" binding:"required"`
	Metadata  string `json:"metadata"`
}

func createLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createLinkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("targetUrl is required"))
			return
		}
		link, err := svc.CreateLink(req.TargetURL, req.Metadata)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"shortCode": link.ShortCode,
			"shortUrl":  svc.ShortURL(link.ShortCode),
			"targetUrl": link.TargetURL,
		})
	}
}

func redirect(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link, err := svc.Resolve(c.Param("code"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Redirect(http.StatusFound, link.TargetURL)
	}
}

func getQRCode(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, err := svc.GetLink(c.Param("code")); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		size := 256
		if sizeParam := c.Query("size"); sizeParam != "" {
			if parsed, err := strconv.Atoi(sizeParam); err == nil {
				size = parsed
			}
		}
		png, err := svc.QRCodePNG(c.Param("code"), size)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Data(http.StatusOK, "image/png", png)
	}
}
