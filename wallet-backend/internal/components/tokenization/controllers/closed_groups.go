package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/services"
)

// registerClosedGroupRoutes wires a PRIVATE offering's own access-group
// management - creating the group RequestMint's guard requires (see
// minting.go), and adding/removing/listing its members by username, the
// original's own convention (users.Service.
// ResolveUsernameToPrimaryWalletAddress's own doc comment).
func registerClosedGroupRoutes(authed *gin.RouterGroup, svc *services.Service) {
	authed.POST("/:assetId/closed-group", createClosedGroup(svc))
	authed.GET("/:assetId/closed-group/members", listClosedGroupMembers(svc))
	authed.POST("/:assetId/closed-group/members", addClosedGroupMember(svc))
	authed.DELETE("/:assetId/closed-group/members/:username", removeClosedGroupMember(svc))
}

type createClosedGroupRequest struct {
	GroupName string `json:"groupName" binding:"required"`
}

func createClosedGroup(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req createClosedGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("groupName is required"))
			return
		}
		group, err := svc.CreatePrivateOfferingGroup(userID, assetID, req.GroupName)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, group)
	}
}

type closedGroupMemberRequest struct {
	Username string `json:"username" binding:"required"`
}

func addClosedGroupMember(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		var req closedGroupMemberRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("username is required"))
			return
		}
		if err := svc.AddPrivateOfferingMember(userID, assetID, req.Username); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func removeClosedGroupMember(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := resolveUserID(c, svc)
		if !ok {
			return
		}
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		if err := svc.RemovePrivateOfferingMember(userID, assetID, c.Param("username")); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func listClosedGroupMembers(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		assetID, ok := assetIDParam(c)
		if !ok {
			return
		}
		usernames, err := svc.ListPrivateOfferingMembers(assetID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, usernames)
	}
}
