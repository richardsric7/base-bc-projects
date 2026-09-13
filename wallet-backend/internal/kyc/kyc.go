// Package kyc abstracts identity-verification providers. The upstream
// project integrates Sumsub and Doja directly into the users component; this
// template instead defines the shape any provider must fill and ships a
// ManualKYCProvider for teams that review documents by hand (or haven't
// picked a vendor yet), so KYC status still works end-to-end without an
// external contract.
package kyc

import "sync"

// Status is the lifecycle a verification moves through.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// Provider is the contract a real KYC vendor integration (Sumsub, Doja,
// Persona, ...) implements.
type Provider interface {
	// StartVerification kicks off a check for userID and returns a
	// provider-specific reference (e.g. a hosted verification URL or
	// applicant ID) the client uses to complete the flow.
	StartVerification(userID string) (reference string, err error)
	// HandleWebhook processes a provider callback payload and returns the
	// userID and new status it reports.
	HandleWebhook(payload []byte) (userID string, status Status, err error)
	// GetStatus returns the last known status for userID.
	GetStatus(userID string) (Status, error)
}

// ManualKYCProvider is a stand-in for teams that review documents by hand:
// StartVerification just records "pending", and status is advanced by an
// admin action (e.g. a future admin endpoint) rather than a vendor webhook.
type ManualKYCProvider struct {
	mu       sync.Mutex
	statuses map[string]Status
}

func NewManualKYCProvider() *ManualKYCProvider {
	return &ManualKYCProvider{statuses: make(map[string]Status)}
}

func (m *ManualKYCProvider) StartVerification(userID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[userID] = StatusPending
	return "manual-review:" + userID, nil
}

func (m *ManualKYCProvider) HandleWebhook([]byte) (string, Status, error) {
	return "", "", errUnsupported
}

func (m *ManualKYCProvider) GetStatus(userID string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statuses[userID]
	if !ok {
		return StatusPending, nil
	}
	return status, nil
}

// SetStatus lets an admin flow record the outcome of a manual review.
func (m *ManualKYCProvider) SetStatus(userID string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[userID] = status
}

var errUnsupported = &unsupportedError{}

type unsupportedError struct{}

func (*unsupportedError) Error() string {
	return "ManualKYCProvider has no webhook to handle; advance status via SetStatus"
}
