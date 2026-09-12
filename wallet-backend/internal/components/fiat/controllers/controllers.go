// Package controllers wires the fiat component's routes: the activation
// quote/invoice/history routes require a wallet-session JWT like the rest
// of the API; the Flutterwave webhook route is deliberately unauthenticated
// (a vendor calls it, not a logged-in user) and authenticates the request
// itself via Flutterwave's shared-secret header - never trust the payload
// without that check passing first.
package controllers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/fiat/models"
	"wallet-backend/internal/components/fiat/services"
	fiatprocessor "wallet-backend/internal/fiat/flutterwave"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/sharedconfig"
)

// Init registers the fiat component's routes on router.
func Init(router *gin.Engine, gc *sharedconfig.GlobalConfig) {
	svc := services.New(gc.DB, gc.Blockchain, gc.Rates, gc.FaucetKeySalt, gc.ActivationRewardTokenSymbol)

	authed := router.Group("/v1/fiat")
	authed.Use(middleware.JWTAuth(gc.JWTSecret, middleware.AudienceWalletSession))
	authed.GET("/activate", getActivationQuote(svc))
	authed.GET("/payments", getPayments(svc))
	authed.POST("/flutterwave/invoices", createInvoice(svc))

	webhooks := router.Group("/v1/callbacks/fiat")
	webhooks.POST("/flutterwave/webhook", flutterwaveWebhook(svc, gc.FlutterwaveSecretHash))
}

func getActivationQuote(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		quote, err := svc.GetActivationQuote(c.GetString(middleware.CtxSubject))
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, quote)
	}
}

func getPayments(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		address := c.GetString(middleware.CtxSubject)
		payments, err := svc.ListPayments(address)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		invoices, err := svc.ListInvoices(address)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"completedPayments": payments, "invoices": invoices})
	}
}

type createInvoiceRequest struct {
	Reference         string  `json:"reference" binding:"required"` // the invoice ID, reused as Flutterwave's tx_ref
	PaymentType       string  `json:"paymentType" binding:"required"`
	Amount            float64 `json:"amount" binding:"required"`
	Currency          string  `json:"currency" binding:"required"`
	SignedTransaction *string `json:"signedTransaction"` // optional: only a payment type that settles on-chain needs one
}

func createInvoice(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createInvoiceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("reference, paymentType, amount and currency are required"))
			return
		}
		invoice, err := svc.CreateInvoice(c.GetString(middleware.CtxSubject), req.Reference, "flutterwave", req.PaymentType, req.Amount, req.Currency, req.SignedTransaction)
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusCreated, invoice)
	}
}

// flutterwaveWebhook verifies Flutterwave's shared-secret "verif-hash"
// header before trusting the payload at all - see
// internal/fiat/flutterwave.VerifySignature's doc comment for the timing
// hardening this applies relative to the original.
func flutterwaveWebhook(svc *services.Service, secretHash string) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			apperrors.Abort(c, apperrors.BadRequest("failed to read request body"))
			return
		}
		if !fiatprocessor.VerifySignature(secretHash, c.GetHeader("verif-hash")) {
			apperrors.Abort(c, apperrors.Unauthorized("invalid webhook signature"))
			return
		}

		var payload models.FlutterwaveWebhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			apperrors.Abort(c, apperrors.BadRequest("invalid webhook payload"))
			return
		}
		svc.LogWebhook(payload.Event, payload.Data.TxRef)

		if payload.Data.Status != "successful" {
			c.JSON(http.StatusOK, "success")
			return
		}

		amount := decimal.NewFromFloat(payload.Data.Amount)
		switch {
		case strings.EqualFold(payload.MetaData.Product, "activation"):
			err = svc.ProcessActivation(c.Request.Context(), payload.MetaData.UserID, payload.Data.TxRef, amount, payload.Data.Currency)
		default:
			// Every other product (e.g. a future "asset purchase") settles
			// through the generic pending-invoice path, keyed by tx_ref -
			// see SettlePendingInvoice's doc comment.
			err = svc.SettlePendingInvoice(c.Request.Context(), payload.Data.TxRef, payload.Data.TxRef, payload.Data.Amount)
		}
		if err != nil {
			apperrors.AbortAny(c, err)
			return
		}
		c.JSON(http.StatusOK, "success")
	}
}
