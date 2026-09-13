// Package notify defines the hook this engine calls whenever it records a
// new PaymentHistory row touching a tracked wallet - see PLAN.md §3.5.
// This engine is uniquely positioned to notice a payment landing on-chain
// regardless of which channel submitted it (self-submitted, received from
// an arbitrary external sender, or submitted via a partner API), which
// wallet-backend's own submit-time notification path cannot do for
// incoming payments. Kept as an independent Provider-interface-plus-Noop-
// default package (the same pattern every optional vendor integration in
// this project family uses) rather than a cross-module import of
// wallet-backend's own internal/notify, since the two services are
// separate deployables.
package notify

// Event is what a Provider receives for one newly-recorded payment.
type Event struct {
	UserID          uint   // wallet-backend's users.User.ID for the tracked-wallet side
	Address         string // the tracked wallet's address (whichever side matched)
	Direction       string // "SENT" or "RECEIVED"
	TransactionType string // PAYMENT | MINT | BURN | SWAP <A>><B>
	Amount          string
	AssetCode       string
	CounterAddress  string // the other side of the transfer
	TransactionHash string
}

// Provider delivers a payment-history notification somewhere a user or
// operator will see it (push notification, webhook, message queue - left
// to whichever implementation is wired in).
type Provider interface {
	NotifyPayment(Event) error
}

// NoopProvider drops every notification - the default until a real
// provider is configured, so the indexer's core behavior never depends on
// one existing.
type NoopProvider struct{}

func NewNoopProvider() Provider { return NoopProvider{} }

func (NoopProvider) NotifyPayment(Event) error { return nil }
