package models

// DojaWebhookEvent is the payload Dojah posts to the KYC webhook. Trimmed
// to the fields this component actually acts on - VerificationStatus,
// WidgetID, the username Dojah echoes back in Metadata.UserID, and the BVN
// value used to trigger fiat-rail onboarding once Phase 5 (Stablerail)
// exists - out of the much larger payload Dojah actually sends (raw
// government/address/AML data this project has no use for). See the
// original's postCallbacksDojaWebhookHandler for the full shape if a future
// phase needs more of it.
type DojaWebhookEvent struct {
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
	IDType             string `json:"id_type"`
	Value              string `json:"value"`
	WidgetID           string `json:"widget_id"`
	VerificationStatus string `json:"verification_status"` // Pending, Completed, or Failed
}
