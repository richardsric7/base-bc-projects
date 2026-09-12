// Package controllers wires HTTP routes to the users services. Handlers
// only parse input, call a service method, and translate the result/error
// to JSON - no business logic here.
package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/users/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the users component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.DB)

	public := router.Group("/v1/users")
	public.GET("/:username", getUser(svc))
	public.GET("/security-questions", listSecurityQuestions(svc))

	authed := router.Group("/v1/users")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.POST("", register(svc))
	authed.DELETE("/:username", deleteUser(svc))
	authed.POST("/security-answers", setSecurityAnswer(svc))
	authed.POST("/security-answers/verify", verifySecurityAnswer(svc))
}

type registerRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required"`
}

// register requires a wallet-session JWT (see internal/components/auth) and
// takes the address to register from that verified session, never from the
// request body - so a caller can only ever register a profile for an
// address they've proven ownership of via SIWE.
func register(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("username and email are required"))
			return
		}
		address := c.GetString(middleware.CtxSubject)
		user, err := svc.Register(services.RegisterInput{Username: req.Username, Email: req.Email, Address: address})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, user)
	}
}

func getUser(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := svc.GetByUsername(c.Param("username"))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

// deleteUser only allows a caller to delete the account whose address
// matches their verified session - never someone else's.
func deleteUser(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		username := c.Param("username")
		user, err := svc.GetByUsername(username)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		if user.Address != c.GetString(middleware.CtxSubject) {
			apperrors.Abort(c, apperrors.Forbidden("you may only delete your own account"))
			return
		}
		if err := svc.Delete(username); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func listSecurityQuestions(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		questions, err := svc.ListSecurityQuestions()
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, questions)
	}
}

type securityAnswerRequest struct {
	SecurityQuestionID uint   `json:"securityQuestionId" binding:"required"`
	Answer             string `json:"answer" binding:"required"`
}

func setSecurityAnswer(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req securityAnswerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("securityQuestionId and answer are required"))
			return
		}
		user, err := svc.GetByAddress(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		if err := svc.SetSecurityAnswer(user.ID, req.SecurityQuestionID, req.Answer); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func verifySecurityAnswer(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req securityAnswerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("securityQuestionId and answer are required"))
			return
		}
		user, err := svc.GetByAddress(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		ok, err := svc.VerifySecurityAnswer(user.ID, req.SecurityQuestionID, req.Answer)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"match": ok})
	}
}
