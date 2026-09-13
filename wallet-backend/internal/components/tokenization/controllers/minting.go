package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/services"
	"wallet-backend/internal/middleware"
)

func registerMintingRoutes(authed *gin.RouterGroup, svc *services.Service) {
	authed.POST("/:assetId/mint-request", requestMint(svc))
	authed.GET("/mint-approvals/:mintApprovalId", getMintApproval(svc))
	authed.POST("/mint-approvals/:mintApprovalId/sign", signMintApproval(svc))
}

func requestMint(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		approval, err := svc.RequestMint(userID, assetID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, approval)
	}
}

func getMintApproval(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		mintApprovalID, ok := idParam(c, "mintApprovalId")
		if !ok {
			return
		}
		approval, err := svc.GetMintApproval(mintApprovalID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		message, err := svc.CanonicalMintMessage(mintApprovalID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"approval": approval, "messageToSign": message})
	}
}

type signMintApprovalRequest struct {
	Signature string `json:"signature" binding:"required"`
}

func signMintApproval(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		mintApprovalID, ok := idParam(c, "mintApprovalId")
		if !ok {
			return
		}
		var req signMintApprovalRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("signature is required"))
			return
		}
		// The approver signs as themselves (their own verified session
		// address) - not the asset's applicant.
		approverAddress := c.GetString(middleware.CtxSubject)
		approval, err := svc.SignMintApproval(c.Request.Context(), mintApprovalID, approverAddress, req.Signature)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, approval)
	}
}
