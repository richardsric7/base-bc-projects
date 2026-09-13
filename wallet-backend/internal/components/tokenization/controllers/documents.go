package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/services"
)

func registerDocumentRoutes(authed *gin.RouterGroup, svc *services.Service) {
	authed.PUT("/:assetId/document", uploadDocument(svc))
	authed.PUT("/:assetId/logo", uploadLogo(svc))
	authed.PUT("/:assetId/fee-proof", uploadFeeProof(svc))
	authed.DELETE("/documents/:documentId", deleteDocument(svc))
	authed.DELETE("/fee-proofs/:documentId", deleteFeeProof(svc))
}

func uploadDocument(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		fileHeader, err := c.FormFile("file")
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("a file is required"))
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("failed to read uploaded file"))
			return
		}
		defer file.Close()

		doc, err := svc.UploadDocument(userID, assetID, true, c.PostForm("documentType"), c.PostForm("documentTitle"), file, fileHeader.Filename)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, doc)
	}
}

func uploadLogo(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		fileHeader, err := c.FormFile("file")
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("a file is required"))
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("failed to read uploaded file"))
			return
		}
		defer file.Close()

		asset, err := svc.UploadLogo(userID, assetID, true, file, fileHeader.Filename)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, asset)
	}
}

func uploadFeeProof(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		fileHeader, err := c.FormFile("file")
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("a file is required"))
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("failed to read uploaded file"))
			return
		}
		defer file.Close()

		proof, err := svc.UploadFeeProofOfPayment(userID, assetID, c.PostForm("paymentMethodId"), c.PostForm("transactionReference"), file, fileHeader.Filename)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, proof)
	}
}

func deleteDocument(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		documentID, ok := idParam(c, "documentId")
		if !ok {
			return
		}
		if err := svc.DeleteDocument(userID, true, documentID); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func deleteFeeProof(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		documentID, ok := idParam(c, "documentId")
		if !ok {
			return
		}
		if err := svc.DeleteFeeProofOfPayment(userID, documentID); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
