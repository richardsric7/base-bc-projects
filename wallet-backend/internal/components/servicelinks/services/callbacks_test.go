package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wallet-backend/internal/components/servicelinks/models"
)

func TestDispatchApprovalCallback_PostsOutcomeToCallbackURL(t *testing.T) {
	var mu sync.Mutex
	var received approvalCallbackPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dispatchApprovalCallback(models.ServiceLinkApproval{
		ID:           "abc123",
		Kind:         models.ApprovalAuthorize,
		TargetUserID: 7,
		Authorized:   true,
		CallbackURL:  srv.URL,
	})

	mu.Lock()
	defer mu.Unlock()
	if received.ApprovalID != "abc123" || !received.Authorized || received.TargetUserID != 7 {
		t.Fatalf("unexpected callback payload: %+v", received)
	}
}

func TestDispatchApprovalCallback_RetriesOnFailureThenSucceeds(t *testing.T) {
	origBackoff := approvalCallbackBackoff
	approvalCallbackBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { approvalCallbackBackoff = origBackoff }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dispatchApprovalCallback(models.ServiceLinkApproval{ID: "retry1", CallbackURL: srv.URL})

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected exactly 3 attempts (2 failures then a success), got %d", got)
	}
}

func TestDispatchApprovalCallback_GivesUpAfterExhaustingRetries(t *testing.T) {
	origBackoff := approvalCallbackBackoff
	approvalCallbackBackoff = []time.Duration{time.Millisecond}
	defer func() { approvalCallbackBackoff = origBackoff }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dispatchApprovalCallback(models.ServiceLinkApproval{ID: "alwaysfails", CallbackURL: srv.URL})

	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("expected exactly 2 attempts (1 backoff entry = 2 total tries), got %d", got)
	}
}

func TestDispatchApprovalCallback_NoCallbackURLIsANoop(t *testing.T) {
	// Must return immediately without panicking or making any request.
	dispatchApprovalCallback(models.ServiceLinkApproval{ID: "none"})
}

func TestApprove_DispatchesCallbackAsynchronously(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanRequestAuthorization: true})
	target := createTestUser(t, db, "targetuser", "0x6666666666666666666666666666666666666666", nil)

	received := make(chan approvalCallbackPayload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload approvalCallbackPayload
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	approval, err := svc.RequestApproval(link.ID, RequestApprovalInput{
		Kind:           models.ApprovalAuthorize,
		TargetUsername: target.Username,
		CallbackURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	if _, err := svc.Approve(approval.ID, target.Address); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	select {
	case payload := <-received:
		if payload.ApprovalID != approval.ID || !payload.Authorized {
			t.Fatalf("unexpected callback payload: %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for the approval callback to be dispatched")
	}
}
