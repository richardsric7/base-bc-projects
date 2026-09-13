// Package fiat defines the contract a fiat payment processor (Flutterwave,
// Stripe, Stablerail, ...) must satisfy to plug into this wallet. Fiat rails
// are too jurisdiction-specific to fake usefully, so unlike notify/kyc this
// package deliberately ships no default implementation - a real project
// picks a processor and implements Processor directly.
//
// Recommended shape when you do implement one, based on the
// TOKENIZED_ASSET_PURCHASE_BY_FIAT.md flow in the upstream project this
// template is derived from: decouple three phases keyed by one
// caller-supplied idempotency reference -
//  1. build and (if needed) partially sign the on-chain leg of the
//     transaction BEFORE the fiat charge starts,
//  2. initiate the charge with the processor using the same reference as
//     its transaction/idempotency key,
//  3. on the processor's webhook confirming payment, submit the pre-built
//     transaction to the chain.
//
// This keeps "the user paid" and "the chain settled" as two independently
// retriable steps instead of one fragile synchronous call chain.
package fiat

import "context"

// ChargeRequest describes a fiat charge to initiate.
type ChargeRequest struct {
	// Reference is a caller-supplied idempotency key, reused as the
	// processor's own transaction reference wherever the provider supports one.
	Reference        string
	AmountMinorUnits int64
	Currency         string
	CustomerEmail    string
}

// ChargeResult is what a processor returns immediately after initiating a charge.
type ChargeResult struct {
	Reference   string
	RedirectURL string // where to send the customer to complete payment, if applicable
}

// WebhookEvent is the normalized result of verifying and parsing a
// processor's webhook payload.
type WebhookEvent struct {
	Reference string
	Succeeded bool
	RawStatus string
}

// Processor is implemented by a concrete fiat payment provider integration.
type Processor interface {
	InitiateCharge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
	VerifyWebhook(signatureHeader string, payload []byte) (WebhookEvent, error)
}
