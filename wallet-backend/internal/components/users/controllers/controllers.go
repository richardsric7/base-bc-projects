// Package controllers wires HTTP routes to the users services. Handlers
// only parse input, call a service method, and translate the result/error
// to JSON - no business logic here.
package controllers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/cache"
	"wallet-backend/internal/components/users/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/network"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the users component's routes on router and returns the
// underlying Service so main.go can wire it into other components that
// need to look up or register users (see servicelinks).
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) *services.Service {
	svc := services.New(gc.DB, gc.Mailer, gc.RecoveryAuthoritySalt, gc.RecoveryOTPTTL, gc.Blockchain, gc.SafeDeployerKeySalt)
	svc.RecoveryOperatorKeySalts = gc.RecoveryOperatorKeySalts
	svc.RecoveryServiceThreshold = gc.RecoveryServiceThreshold
	svc.WalletRecoveryFeeWei = gc.WalletRecoveryFeeWei
	svc.ChainID = gc.ChainID

	public := router.Group("/v1/users")
	public.GET("/:username", getUser(svc, gc.Cache, gc.AddressWatcher))
	public.GET("/security-questions", listSecurityQuestions(svc))

	authed := router.Group("/v1/users")
	authed.Use(middleware.SignatureAuth(gc.DB, gc.SignatureAuthToleranceSeconds))
	authed.GET("/me", getMe(svc))
	authed.POST("", register(svc))
	authed.POST("/wallet/deploy", deployPrimaryWallet(svc))
	authed.DELETE("/:username", deleteUser(svc))
	authed.POST("/security-answers", setSecurityAnswer(svc))
	authed.POST("/security-answers/verify", verifySecurityAnswer(svc))
	authed.POST("/account-recovery", enableAccountRecovery(svc))
	authed.DELETE("/account-recovery", disableAccountRecovery(svc))

	// Account recovery is deliberately unauthenticated - see recovery.go's
	// package doc comment for why: its purpose is helping someone who can
	// no longer produce a SIWE signature at all.
	recovery := router.Group("/v1/account-recovery")
	recovery.POST("/:username/request-otp", requestRecoveryOTP(svc))
	recovery.POST("/:username/recover", recoverAccount(svc))

	registerWalletRecoveryRoutes(authed, recovery, svc)

	return svc
}

type registerRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required"`
}

// register requires a signed request (middleware.SignatureAuth, PLAN.md
// §12) and takes the signer EOA to register from that verified request,
// never from the request body - so a caller can only ever register a
// profile keyed on an EOA they've proven ownership of via personal_sign.
// Since no wallet exists yet to name in X-Wallet-Address, a registration
// request is necessarily self-signed (signer == wallet, both the
// caller's own EOA) - CtxSigner and CtxSubject are the same value here,
// but CtxSigner is used to make explicit that this is the raw key
// Register will compute a primary wallet Safe address from (PLAN.md
// §13.2), not a wallet address itself. Registration is just another
// signed request, with no separate auth step first (PLAN.md §12.6).
func register(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("username and email are required"))
			return
		}
		signerAddress := c.GetString(middleware.CtxSigner)
		user, err := svc.Register(services.RegisterInput{Username: req.Username, Email: req.Email, SignerAddress: signerAddress, RegistrationIP: c.ClientIP()})
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, user)
	}
}

// deployPrimaryWallet submits the on-chain deployment of the caller's own
// primary wallet Safe (services.DeployPrimaryWallet) - idempotent, so a
// client can call it freely (e.g. right after a funding/activation step)
// without worrying about double-deploying.
func deployPrimaryWallet(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := svc.DeployPrimaryWallet(c.Request.Context(), c.GetString(middleware.CtxSigner))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

// userResponseCacheTTL is the fallback expiry for a cached GET /v1/users/:username
// response. AddressWatcher normally invalidates the entry the moment
// something on-chain changes for the user's address (a Transfer or
// Approval touching it), so this TTL is only a backstop against a missed
// or delayed poll - see PLAN.md §2's "Horizon operation streaming" row.
const userResponseCacheTTL = 5 * time.Minute

func getUser(svc *services.Service, respCache cache.Cache, watcher *network.AddressWatcher) gin.HandlerFunc {
	return func(c *gin.Context) {
		username := c.Param("username")
		cacheKey := "users:get:" + username
		if cached, ok := respCache.Get(cacheKey); ok {
			c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cached))
			return
		}

		user, err := svc.GetByUsername(username)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}

		if body, marshalErr := json.Marshal(user); marshalErr == nil {
			respCache.Set(cacheKey, string(body), userResponseCacheTTL)
			watcher.Watch(user.Address, func() { respCache.Delete(cacheKey) })
		}
		c.JSON(http.StatusOK, user)
	}
}

// getMe looks a profile up by the verified signer's own address rather
// than a username - onboarding needs this to tell "this signer already
// has a registered profile" from "this is a brand-new signer" without
// knowing a username to ask for by name (GET /v1/users/:username can't
// help there: the client only ever knows the signer's address at this
// point, never their username).
func getMe(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := svc.GetByAddress(c.GetString(middleware.CtxSubject))
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

func enableAccountRecovery(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := svc.GetByAddress(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		if err := svc.EnableAccountRecovery(user.ID); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func disableAccountRecovery(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := svc.GetByAddress(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		if err := svc.DisableAccountRecovery(user.ID); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// requestRecoveryOTP always responds 204 regardless of whether the
// username exists or has recovery enabled - see services.RequestRecoveryOTP.
func requestRecoveryOTP(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.RequestRecoveryOTP(c.Param("username")); err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type recoverAccountRequest struct {
	NewAddress          string                  `json:"newAddress" binding:"required"`
	NewAddressSignature string                  `json:"newAddressSignature" binding:"required"`
	OTP                 string                  `json:"otp" binding:"required"`
	Answers             []securityAnswerRequest `json:"answers" binding:"required"`
}

// recoverAccount expects newAddressSignature to be a personal_sign
// signature, produced by newAddress's own key, over
// services.RecoveryMessage(username, newAddress) - proving the caller
// controls the address they're asking the account to be re-pointed to,
// the same way Register proves control of an address via SIWE.
func recoverAccount(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req recoverAccountRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("newAddress, newAddressSignature, otp and answers are required"))
			return
		}
		answers := make([]services.SecurityAnswerInput, len(req.Answers))
		for i, a := range req.Answers {
			answers[i] = services.SecurityAnswerInput{SecurityQuestionID: a.SecurityQuestionID, Answer: a.Answer}
		}
		logEntry, err := svc.Recover(c.Param("username"), req.NewAddress, req.NewAddressSignature, req.OTP, answers)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, logEntry)
	}
}
