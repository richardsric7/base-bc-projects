package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"wallet-backend/internal/components/servicelinks/models"
)

// approvalCallbackClient is a package-level http.Client with a bounded
// timeout so a slow or hanging partner endpoint can never leak a
// goroutine indefinitely.
var approvalCallbackClient = &http.Client{Timeout: 10 * time.Second}

// approvalCallbackBackoff is this port's first retried-webhook dispatch
// (PLAN.md §14.2 item 3) - the original's channel-based
// callBackRetryChan has no existing generic retry mechanism elsewhere in
// this codebase to reuse (confirmed by research: internal/notify and
// internal/alerting are both synchronous, best-effort, no-retry), so
// this is a small fixed-attempt, fixed-backoff loop rather than a
// literal port of that channel design. Var, not const, so tests can
// shrink it.
var approvalCallbackBackoff = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}

type approvalCallbackPayload struct {
	ApprovalID   string `json:"approvalId"`
	Kind         string `json:"kind"`
	TargetUserID uint   `json:"targetUserId"`
	Authorized   bool   `json:"authorized"`
}

// dispatchApprovalCallback POSTs the approval's outcome to its
// CallbackURL, retrying with backoff on failure - the piece that lets a
// partner learn "approved" without polling VerifyApproval in a loop
// (PLAN.md §14.2 item 3). Always called in its own goroutine from
// Approve so a slow or unreachable partner endpoint never blocks or
// fails the user's own approve request; only the final failure (after
// every retry is exhausted) is logged, the same fire-and-forget-with-
// logging convention already established by sharedaccess's
// ReconcileRelayers.
func dispatchApprovalCallback(approval models.ServiceLinkApproval) {
	if approval.CallbackURL == "" {
		return
	}
	body, err := json.Marshal(approvalCallbackPayload{
		ApprovalID:   approval.ID,
		Kind:         string(approval.Kind),
		TargetUserID: approval.TargetUserID,
		Authorized:   approval.Authorized,
	})
	if err != nil {
		log.Printf("[servicelinks] failed to encode callback payload for approval %s: %v", approval.ID, err)
		return
	}

	attempts := len(approvalCallbackBackoff) + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(approvalCallbackBackoff[attempt-1])
		}
		lastErr = postApprovalCallback(approval.CallbackURL, body)
		if lastErr == nil {
			return
		}
	}
	log.Printf("[servicelinks] callback delivery for approval %s to %s failed after %d attempts: %v", approval.ID, approval.CallbackURL, attempts, lastErr)
}

func postApprovalCallback(callbackURL string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("invalid callback URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := approvalCallbackClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback endpoint returned status %d", resp.StatusCode)
	}
	return nil
}
