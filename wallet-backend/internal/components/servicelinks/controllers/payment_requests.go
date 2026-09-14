package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/components/servicelinks/services"
)

// requestPaymentLink is the app-signed half of the payment-request-link
// pair (PLAN.md §14.1a/§14.2 item 1) - a wallet owner asking for their
// own "receive payment" QR, prefilled with the to/tokenAddress/amount/
// memo they supply. This is not an approval and creates no
// ServiceLinkApproval row: see services.RequestPaymentLink's doc comment
// for why this route has no separate identity check to worry about
// (unlike the original's dead, never-enforced check) and no
// CanSendPayments gate (there's no ServiceLink/partner context here to
// check that capability against).
func requestPaymentLink(svc *services.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := svc.RequestPaymentLink(c.Query("to"), c.Query("tokenAddress"), c.Query("amount"), c.Query("memo"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
