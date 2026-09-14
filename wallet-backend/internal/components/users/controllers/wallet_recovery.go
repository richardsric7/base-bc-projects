package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/users/services"
	"wallet-backend/internal/middleware"
)

// registerWalletRecoveryRoutes wires PLAN.md §15's wallet-recovery Branch
// B routes. Enable/disable fit §12's normal self-service model (the
// caller must already be the wallet's current signer, same as every
// other authed route in this file) and live under the existing authed
// /v1/users group; the actual recovery-execution route is the deliberate
// exception §15.6 calls for and lives alongside Branch A's under
// /v1/account-recovery instead, unauthenticated for the same reason
// recoverAccount is - the caller controls no currently-authorized key at
// all.
func registerWalletRecoveryRoutes(authed *gin.RouterGroup, recovery *gin.RouterGroup, svc *services.Service) {
	authed.POST("/wallet-recovery/enable", buildEnableWalletRecovery(svc))
	authed.POST("/wallet-recovery/enable/confirm", confirmEnableWalletRecovery(svc))
	authed.POST("/wallet-recovery/disable", buildDisableWalletRecovery(svc))
	authed.POST("/wallet-recovery/disable/confirm", confirmDisableWalletRecovery(svc))

	recovery.POST("/:username/recover-wallet", recoverWallet(svc))
}

func buildEnableWalletRecovery(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		challenge, err := svc.BuildEnableWalletRecovery(c.Request.Context(), c.GetString(middleware.CtxSigner))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, challenge)
	}
}

type confirmEnableWalletRecoveryRequest struct {
	FeeTxHash         string `json:"feeTxHash"`
	AddOwnerSignature string `json:"addOwnerSignature" binding:"required"`
	SetGuardSignature string `json:"setGuardSignature" binding:"required"`
}

// confirmEnableWalletRecovery expects addOwnerSignature/setGuardSignature
// to be personal_sign signatures, produced by the caller's own current
// signer key, over the two SafeTxHash values buildEnableWalletRecovery
// returned - and, if that response included a feeTx, feeTxHash is the
// hash of that transaction once the caller signed and submitted it
// themselves (trusted once supplied, matching this codebase's posture
// for every other self-submitted payment - see patron's
// ConfirmSubscription).
func confirmEnableWalletRecovery(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req confirmEnableWalletRecoveryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("addOwnerSignature and setGuardSignature are required"))
			return
		}
		user, err := svc.ConfirmEnableWalletRecovery(c.Request.Context(), c.GetString(middleware.CtxSigner), req.FeeTxHash, req.AddOwnerSignature, req.SetGuardSignature)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

func buildDisableWalletRecovery(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		challenge, err := svc.BuildDisableWalletRecovery(c.Request.Context(), c.GetString(middleware.CtxSigner))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, challenge)
	}
}

type confirmDisableWalletRecoveryRequest struct {
	RemoveOwnerSignature string `json:"removeOwnerSignature" binding:"required"`
	ClearGuardSignature  string `json:"clearGuardSignature" binding:"required"`
}

func confirmDisableWalletRecovery(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req confirmDisableWalletRecoveryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("removeOwnerSignature and clearGuardSignature are required"))
			return
		}
		user, err := svc.ConfirmDisableWalletRecovery(c.Request.Context(), c.GetString(middleware.CtxSigner), req.RemoveOwnerSignature, req.ClearGuardSignature)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

type recoverWalletRequest struct {
	NewSignerAddress          string                  `json:"newSignerAddress" binding:"required"`
	NewSignerAddressSignature string                  `json:"newSignerAddressSignature" binding:"required"`
	OTP                       string                  `json:"otp" binding:"required"`
	Answers                   []securityAnswerRequest `json:"answers" binding:"required"`
}

// recoverWallet is Branch B's execution route (PLAN.md §15.9 Phase 4) -
// recovery.go's existing POST /v1/account-recovery/:username/recover
// (Branch A) is completely untouched by this. newSignerAddressSignature
// is a personal_sign signature, produced by newSignerAddress's own key,
// over services.RecoveryMessage(username, newSignerAddress) - the same
// proof-of-ownership shape recoverAccount already requires for Branch A,
// reused as-is (services.RecoverWallet calls the same
// verifyNewAddressOwnership helper internally).
func recoverWallet(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req recoverWalletRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("newSignerAddress, newSignerAddressSignature, otp and answers are required"))
			return
		}
		answers := make([]services.SecurityAnswerInput, len(req.Answers))
		for i, a := range req.Answers {
			answers[i] = services.SecurityAnswerInput{SecurityQuestionID: a.SecurityQuestionID, Answer: a.Answer}
		}
		logEntry, err := svc.RecoverWallet(c.Param("username"), req.NewSignerAddress, req.NewSignerAddressSignature, req.OTP, answers)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, logEntry)
	}
}
