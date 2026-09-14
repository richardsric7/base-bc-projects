package services

import "wallet-backend/internal/apperrors"

// PaymentRequestLink is the stateless "receive payment" QR/deep-link a
// caller mints - not an approval (PLAN.md §14.1a): there is no
// ServiceLinkApproval row and no verify/redeem step. The wallet owner
// reviews and signs the resulting payment themselves through the
// ordinary payment flow when they scan it; this only prefills that
// flow's destination/asset/amount/memo, the EVM-terms replacement for
// the original's paymentDestination/assetCode/assetIssuer/amount/memo.
type PaymentRequestLink struct {
	To           string `json:"to"`
	TokenAddress string `json:"tokenAddress,omitempty"`
	Amount       string `json:"amount"`
	Memo         string `json:"memo,omitempty"`
	ShortURL     string `json:"shortUrl,omitempty"`
	QRURL        string `json:"qrUrl,omitempty"`
}

// RequestPaymentLink mints a payment-request link for the signed-in
// wallet app user themselves - the app-signed half of the route pair
// (PLAN.md §14.2 item 1). Unlike the original's app-signed route (which
// took a :targetUser path parameter but never actually verified the
// caller matched it - a dead, commented-out check per PLAN.md §14.1a),
// this deliberately has no targetUser parameter at all: the caller can
// only ever request a link for themselves, a tighter design than
// reviving that no-op check. There is no CanSendPayments-style gate
// here either, because this path carries no ServiceLink/partner context
// to check it against - it's an ordinary wallet feature (generate my own
// "receive payment" QR), not a partner-scoped action; CanSendPayments
// gates the partner-side RequestPaymentLinkForOwnedUser below instead,
// where that capability model actually applies.
func (s *Service) RequestPaymentLink(to, tokenAddress, amount, memo string) (*PaymentRequestLink, error) {
	return s.buildPaymentRequestLink(to, tokenAddress, amount, memo)
}

// RequestPaymentLinkForOwnedUser mints a payment-request link on behalf
// of a service link's owned user - the partner-side half of the route
// pair. requireOwnedUser is the same ownership scoping every other
// partner route in this file uses; to is still supplied explicitly
// rather than derived from the owned user's address, matching the
// original's own shape (the destination and the user the request is
// "for" are independent - e.g. a merchant requesting payment to its own
// treasury address on a customer's behalf).
func (s *Service) RequestPaymentLinkForOwnedUser(serviceLinkID, userID uint, to, tokenAddress, amount, memo string) (*PaymentRequestLink, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, err
	}
	return s.buildPaymentRequestLink(to, tokenAddress, amount, memo)
}

func (s *Service) buildPaymentRequestLink(to, tokenAddress, amount, memo string) (*PaymentRequestLink, error) {
	if to == "" || amount == "" {
		return nil, apperrors.BadRequest("to and amount are required")
	}
	result := &PaymentRequestLink{To: to, TokenAddress: tokenAddress, Amount: amount, Memo: memo}

	link, err := s.mintDeepLink("payment", map[string]string{
		"to":           to,
		"tokenAddress": tokenAddress,
		"amount":       amount,
		"memo":         memo,
	}, map[string]string{
		"action": "payment",
		"to":     to,
	})
	if err != nil {
		return nil, err
	}
	result.ShortURL = link.ShortURL
	result.QRURL = link.QRURL
	return result, nil
}
