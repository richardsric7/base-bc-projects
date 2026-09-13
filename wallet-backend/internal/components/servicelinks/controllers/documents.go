package controllers

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/services"
)

// registerDocumentRoutes registers the stakeholder-document store. Every
// route is scoped to the calling service link's own documents by
// ServiceLinkID (checked inside the service layer) - the original had no
// per-tenant ownership record here at all, letting any service link read
// or delete any other tenant's documents by guessing an ID (PLAN.md §4.11
// finding 8). Available to any verified service link regardless of its
// other capabilities, since stakeholder paperwork is about the partner's
// own onboarding, not about a specific end user's wallet.
func registerDocumentRoutes(partner *gin.RouterGroup, svc *services.Service) {
	partner.POST("/documents", uploadStakeholderDocument(svc))
	partner.GET("/documents/:id", getStakeholderDocument(svc))
	partner.DELETE("/documents/:id", deleteStakeholderDocument(svc))
}

func uploadStakeholderDocument(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		fileHeader, err := c.FormFile("file")
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("a file is required"))
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			apperrors.Abort(c, apperrors.Internal("failed to read uploaded file"))
			return
		}
		defer file.Close()

		doc, err := svc.UploadStakeholderDocument(link.ID, fileHeader.Filename, fileHeader.Header.Get("Content-Type"), file)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusCreated, doc)
	}
}

func getStakeholderDocument(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		doc, content, err := svc.GetStakeholderDocument(link.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		defer content.Close()
		c.Header("Content-Disposition", "attachment; filename=\""+doc.OriginalFilename+"\"")
		contentType := doc.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Status(http.StatusOK)
		c.Writer.Header().Set("Content-Type", contentType)
		if _, err := io.Copy(c.Writer, content); err != nil {
			return
		}
	}
}

func deleteStakeholderDocument(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		if err := svc.DeleteStakeholderDocument(link.ID, c.Param("id")); err != nil {
			writeError(c, err)
			return
		}
		noContent(c)
	}
}
