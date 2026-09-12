package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/models"
	"wallet-backend/internal/components/servicelinks/services"
	"wallet-backend/internal/middleware"
)

// registerApprovalRoutes registers the partner-facing half of the
// login/authorize/event consent flow - requesting a user's consent and
// later redeeming it once granted. The end-user-facing half (viewing and
// approving a pending request) is registered directly in Init under
// /v1/approvals, since it authenticates with a wallet-session JWT rather
// than an API key.
func registerApprovalRoutes(partner *gin.RouterGroup, svc *services.Service) {
	partner.POST("/approvals", requestApproval(svc))
	partner.GET("/approvals/:id/verify", verifyApproval(svc))
}

type requestApprovalRequest struct {
	Kind           string `json:"kind" binding:"required"`
	TargetUsername string `json:"targetUsername" binding:"required"`
	Description    string `json:"description"`
	CallbackURL    string `json:"callbackUrl"`
}

// requestApproval is gated per Kind: LOGIN needs CanLogin, AUTHORIZE needs
// CanRequestAuthorization, EVENT needs CanRegisterEvents - the granular
// capabilities that replace the original's single overloaded permission
// flag (PLAN.md §4.11 finding 4).
func requestApproval(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req requestApprovalRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("kind and targetUsername are required"))
			return
		}
		link := currentServiceLink(c)
		kind := models.ApprovalKind(req.Kind)
		switch kind {
		case models.ApprovalLogin:
			if !requireCapability(c, link.CanLogin) {
				return
			}
		case models.ApprovalAuthorize:
			if !requireCapability(c, link.CanRequestAuthorization) {
				return
			}
		case models.ApprovalEvent:
			if !requireCapability(c, link.CanRegisterEvents) {
				return
			}
		default:
			apperrors.Abort(c, apperrors.BadRequest("kind must be LOGIN, AUTHORIZE or EVENT"))
			return
		}

		approval, err := svc.RequestApproval(link.ID, services.RequestApprovalInput{
			Kind:           kind,
			TargetUsername: req.TargetUsername,
			Description:    req.Description,
			CallbackURL:    req.CallbackURL,
		})
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusCreated, approval)
	}
}

func verifyApproval(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		link := currentServiceLink(c)
		approval, sessionToken, err := svc.VerifyApproval(link.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		resp := gin.H{"approval": approval}
		if sessionToken != "" {
			resp["sessionToken"] = sessionToken
		}
		c.JSON(http.StatusOK, resp)
	}
}

// getApprovalForUser lets the signed-in user this approval names see what
// they're being asked to approve before deciding.
func getApprovalForUser(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		approval, err := svc.GetApproval(c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		target, err := svc.Users.GetByID(approval.TargetUserID)
		if err != nil {
			writeError(c, err)
			return
		}
		if target.Address != c.GetString(middleware.CtxSubject) {
			apperrors.Abort(c, apperrors.Forbidden("this approval request is not addressed to you"))
			return
		}
		c.JSON(http.StatusOK, approval)
	}
}

func approveForUser(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		approval, err := svc.Approve(c.Param("id"), c.GetString(middleware.CtxSubject))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, approval)
	}
}
