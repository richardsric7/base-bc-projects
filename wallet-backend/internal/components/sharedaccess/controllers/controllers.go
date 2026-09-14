// Package controllers wires the shared/multi-party wallet access routes.
// Every route requires a signed request (middleware.SignatureAuth,
// PLAN.md §12); the caller's address is read from that verified request,
// never from the request body, so a caller can never act as a group
// member they aren't.
package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/sharedaccess/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the sharedaccess component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.Blockchain, gc.SafeDeployerKeySalt, gc.ChainID, gc.RelayerPool)

	group := router.Group("/v1/shared-access")
	group.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))

	group.POST("/groups", createGroup(svc))
	group.GET("/groups/:groupId", getGroup(svc))
	group.GET("/balance/:groupId", getBalance(svc))
	group.GET("/balance/:groupId/curated", getCuratedBalances(svc))
	group.GET("/wallets", listWallets(svc))

	group.POST("/actions", proposeAction(svc))
	group.GET("/actions", listPending(svc))
	group.GET("/actions/:actionId", getAction(svc))
	group.POST("/actions/:actionId/approve", approveAction(svc))
	group.POST("/actions/:actionId/reject", rejectAction(svc))

	// Group-management proposals (PLAN.md §13.10 Phase 5) - each returns a
	// PendingAction that goes through the exact same approve/reject/
	// execute routes above, not a separate pipeline.
	group.POST("/groups/:groupId/members", proposeAddMember(svc))
	group.POST("/groups/:groupId/members/:memberAddress/remove", proposeRemoveMember(svc))
	group.POST("/groups/:groupId/threshold", proposeChangeThreshold(svc))
	group.POST("/groups/:groupId/disable", proposeDisableGroup(svc))

	return svc
}

type memberInput struct {
	Address string `json:"address" binding:"required"`
	Role    string `json:"role" binding:"required"`
}

type createGroupRequest struct {
	Name      string        `json:"name" binding:"required"`
	Threshold int           `json:"threshold" binding:"required"`
	Members   []memberInput `json:"members" binding:"required"`
}

func createGroup(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createGroupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("name, threshold and members are required"))
			return
		}
		members := make([]services.MemberInput, len(req.Members))
		for i, m := range req.Members {
			members[i] = services.MemberInput{Address: m.Address, Role: models.GroupRole(m.Role)}
		}
		group, err := svc.CreateGroup(c.Request.Context(), req.Name, req.Threshold, members)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, group)
	}
}

func getGroup(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseID(c, "groupId")
		if err != nil {
			return
		}
		group, err := svc.GetGroup(groupID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, group)
	}
}

func getBalance(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseID(c, "groupId")
		if err != nil {
			return
		}
		caller := c.GetString(middleware.CtxSubject)
		balance, err := svc.Balance(c.Request.Context(), groupID, caller, c.Query("token"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"balance": balance})
	}
}

func getCuratedBalances(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseID(c, "groupId")
		if err != nil {
			return
		}
		caller := c.GetString(middleware.CtxSubject)
		balances, err := svc.CuratedBalances(c.Request.Context(), groupID, caller)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, balances)
	}
}

func listWallets(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		caller := c.GetString(middleware.CtxSubject)
		wallets, err := svc.ListWalletsForMember(caller)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, wallets)
	}
}

type proposeActionRequest struct {
	GroupID      uint   `json:"groupId" binding:"required"`
	Kind         string `json:"kind" binding:"required"` // "payment" or "contract_call"/"swap"
	Description  string `json:"description"`
	Recipient    string `json:"recipient"`    // payment only
	TokenAddress string `json:"tokenAddress"` // payment: empty = native; contract_call: unused
	Amount       string `json:"amount"`       // payment only, base-unit decimal string
	To           string `json:"to"`           // contract_call/swap only
	ValueWei     string `json:"valueWei"`     // contract_call/swap only
	Data         string `json:"data"`         // contract_call/swap only, 0x-prefixed calldata
}

func proposeAction(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req proposeActionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("groupId and kind are required"))
			return
		}
		proposer := c.GetString(middleware.CtxSubject)

		var action *models.PendingAction
		var err error
		switch models.ActionKind(req.Kind) {
		case models.ActionPayment:
			action, err = svc.ProposePayment(c.Request.Context(), proposer, req.GroupID, req.Description, req.Recipient, req.TokenAddress, req.Amount, "", "")
		case models.ActionSwap, models.ActionContractCall:
			action, err = svc.ProposeContractCall(c.Request.Context(), proposer, req.GroupID, models.ActionKind(req.Kind), req.Description, req.To, req.ValueWei, req.Data, "", "")
		default:
			apperrors.Abort(c, apperrors.BadRequest(`kind must be "payment", "swap" or "contract_call"`))
			return
		}
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, action)
	}
}

func listPending(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		caller := c.GetString(middleware.CtxSubject)
		actions, err := svc.ListPendingForMember(caller)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, actions)
	}
}

// actionDetail bundles the action with the exact digest the calling member
// must personal_sign to approve it, so a client never has to reconstruct
// the real SafeTxHash (or its nested EIP-1271 wrapping) itself.
type actionDetail struct {
	*models.PendingAction
	DigestToSign string `json:"digestToSign"`
}

func getAction(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actionID, err := parseID(c, "actionId")
		if err != nil {
			return
		}
		action, err := svc.GetAction(actionID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		caller := c.GetString(middleware.CtxSubject)
		digest, err := svc.DigestToSign(actionID, caller)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, actionDetail{PendingAction: action, DigestToSign: digest})
	}
}

type approveRequest struct {
	Signature string `json:"signature" binding:"required"`
}

func approveAction(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actionID, err := parseID(c, "actionId")
		if err != nil {
			return
		}
		var req approveRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("signature is required"))
			return
		}
		caller := c.GetString(middleware.CtxSubject)
		action, err := svc.ApproveAction(c.Request.Context(), actionID, caller, req.Signature)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, action)
	}
}

type rejectRequest struct {
	Reason string `json:"reason" binding:"required"`
}

func rejectAction(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actionID, err := parseID(c, "actionId")
		if err != nil {
			return
		}
		var req rejectRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("reason is required"))
			return
		}
		caller := c.GetString(middleware.CtxSubject)
		action, err := svc.RejectAction(actionID, caller, req.Reason)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, action)
	}
}

type addMemberRequest struct {
	Address      string `json:"address" binding:"required"`
	Role         string `json:"role" binding:"required"`
	NewThreshold int    `json:"newThreshold" binding:"required"`
}

func proposeAddMember(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseID(c, "groupId")
		if err != nil {
			return
		}
		var req addMemberRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("address, role and newThreshold are required"))
			return
		}
		proposer := c.GetString(middleware.CtxSubject)
		action, err := svc.ProposeAddMember(c.Request.Context(), proposer, groupID, req.Address, models.GroupRole(req.Role), req.NewThreshold)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, action)
	}
}

type removeMemberRequest struct {
	NewThreshold int `json:"newThreshold" binding:"required"`
}

func proposeRemoveMember(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseID(c, "groupId")
		if err != nil {
			return
		}
		memberAddress := c.Param("memberAddress")
		var req removeMemberRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("newThreshold is required"))
			return
		}
		proposer := c.GetString(middleware.CtxSubject)
		action, err := svc.ProposeRemoveMember(c.Request.Context(), proposer, groupID, memberAddress, req.NewThreshold)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, action)
	}
}

type changeThresholdRequest struct {
	NewThreshold int `json:"newThreshold" binding:"required"`
}

func proposeChangeThreshold(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseID(c, "groupId")
		if err != nil {
			return
		}
		var req changeThresholdRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("newThreshold is required"))
			return
		}
		proposer := c.GetString(middleware.CtxSubject)
		action, err := svc.ProposeChangeThreshold(c.Request.Context(), proposer, groupID, req.NewThreshold)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, action)
	}
}

func proposeDisableGroup(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseID(c, "groupId")
		if err != nil {
			return
		}
		proposer := c.GetString(middleware.CtxSubject)
		action, err := svc.ProposeDisableGroup(c.Request.Context(), proposer, groupID)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, action)
	}
}

func parseID(c *gin.Context, param string) (uint, error) {
	id, err := strconv.ParseUint(c.Param(param), 10, 64)
	if err != nil {
		apperrors.Abort(c, apperrors.BadRequest("invalid "+param))
		return 0, err
	}
	return uint(id), nil
}
