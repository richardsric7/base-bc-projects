// Package flutterwave implements internal/fiat.Processor against
// Flutterwave's actual v3 API (https://developer.flutterwave.com/reference).
package flutterwave

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"wallet-backend/internal/fiat"
)

const baseURL = "https://api.flutterwave.com/v3"

// Processor implements fiat.Processor against Flutterwave.
type Processor struct {
	SecretKey  string // Bearer token for outbound API calls
	SecretHash string // the shared secret Flutterwave echoes back in the "verif-hash" webhook header
	HTTPClient *http.Client
}

func New(secretKey, secretHash string) *Processor {
	return &Processor{
		SecretKey:  secretKey,
		SecretHash: secretHash,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// InitiateCharge starts a Flutterwave "Standard" charge, returning a hosted
// checkout URL to redirect the customer to. This project's actual
// activation/asset-purchase flow has the client collect payment directly
// via Flutterwave's own SDK and only tells this backend about it
// afterwards (see internal/components/fiat), so nothing in this port
// currently calls InitiateCharge - it's implemented for interface
// completeness and for a project that does want a server-initiated charge.
func (p *Processor) InitiateCharge(ctx context.Context, req fiat.ChargeRequest) (fiat.ChargeResult, error) {
	if p.SecretKey == "" {
		return fiat.ChargeResult{}, fmt.Errorf("flutterwave is not configured (FLUTTERWAVE_SECRET_KEY)")
	}

	body, err := json.Marshal(map[string]interface{}{
		"tx_ref":       req.Reference,
		"amount":       req.AmountMinorUnits,
		"currency":     req.Currency,
		"redirect_url": "",
		"customer": map[string]string{
			"email": req.CustomerEmail,
		},
	})
	if err != nil {
		return fiat.ChargeResult{}, fmt.Errorf("encode charge request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/payments", bytes.NewReader(body))
	if err != nil {
		return fiat.ChargeResult{}, fmt.Errorf("build charge request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.SecretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.HTTPClient.Do(httpReq)
	if err != nil {
		return fiat.ChargeResult{}, fmt.Errorf("reach flutterwave: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fiat.ChargeResult{}, fmt.Errorf("read flutterwave response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return fiat.ChargeResult{}, fmt.Errorf("flutterwave returned %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		Data struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return fiat.ChargeResult{}, fmt.Errorf("decode flutterwave response: %w", err)
	}

	return fiat.ChargeResult{Reference: req.Reference, RedirectURL: parsed.Data.Link}, nil
}

// VerifyWebhook checks signatureHeader (Flutterwave's "verif-hash" header)
// against SecretHash and, if it matches, parses just enough of payload to
// satisfy fiat.WebhookEvent. internal/components/fiat parses the full
// payload itself for the extra fields (product type, amount, currency) its
// business logic needs - see that component's doc comment for why.
//
// ***Hardened relative to the original***: the original compared this
// header with Go's `==` operator (`hash == pcc.VerificationHash`), a
// non-constant-time comparison of a secret value against
// attacker-controlled input. This uses crypto/subtle.ConstantTimeCompare
// instead, closing the (admittedly narrow, since the values are short and
// network jitter dominates) timing side-channel.
func (p *Processor) VerifyWebhook(signatureHeader string, payload []byte) (fiat.WebhookEvent, error) {
	if !VerifySignature(p.SecretHash, signatureHeader) {
		return fiat.WebhookEvent{}, fmt.Errorf("invalid webhook signature")
	}

	var event struct {
		Data struct {
			TxRef  string `json:"tx_ref"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return fiat.WebhookEvent{}, fmt.Errorf("decode webhook payload: %w", err)
	}

	return fiat.WebhookEvent{
		Reference: event.Data.TxRef,
		Succeeded: event.Data.Status == "successful",
		RawStatus: event.Data.Status,
	}, nil
}

// VerifySignature reports whether headerValue matches secretHash, using a
// constant-time comparison (see VerifyWebhook's doc comment). Exported so
// internal/components/fiat's own webhook handler - which needs to parse
// more of the payload than fiat.WebhookEvent carries - can reuse the exact
// same check rather than re-implementing it.
func VerifySignature(secretHash, headerValue string) bool {
	if secretHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(secretHash), []byte(headerValue)) == 1
}
