// Package services implements the swaps component: converting one Stellar
// asset into another via a strict-send path payment. There is no on-disk
// model here - a swap is just a payment-history-shaped event, so it reuses
// the payments component's history rather than duplicating a table; a
// project that needs swap-specific reporting can add its own model later.
package services

import (
	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"

	"github.com/stellar/go-stellar-sdk/txnbuild"
)

type Service struct {
	Blockchain *network.Client
}

func New(blockchain *network.Client) *Service {
	return &Service{Blockchain: blockchain}
}

// BuildSwapXDR builds an unsigned path-payment-strict-send transaction that
// converts sendAmount of the send asset into at least destMin of the
// destination asset, credited back to the same account. Pass path for a
// caller-supplied route (e.g. from a prior call to Horizon's
// /paths/strict-send endpoint); nil requests a direct conversion.
func (s *Service) BuildSwapXDR(publicKey string, sendCode, sendIssuer, sendAmount, destCode, destIssuer, destMin string, path []txnbuild.Asset) (string, error) {
	if !validators.IsValidStellarPublicKey(publicKey) {
		return "", apperrors.BadRequest("invalid Stellar public key")
	}
	xdrString, err := s.Blockchain.BuildPathPaymentStrictSendXDR(publicKey, publicKey, sendCode, sendIssuer, sendAmount, destCode, destIssuer, destMin, path)
	if err != nil {
		return "", apperrors.BadRequest(err.Error())
	}
	return xdrString, nil
}

// SubmitSwap submits a client-signed swap transaction and returns its hash.
func (s *Service) SubmitSwap(signedXDR string) (string, error) {
	tx, err := s.Blockchain.SubmitSignedTransaction(signedXDR)
	if err != nil {
		return "", apperrors.BadRequest("transaction rejected by the network: " + err.Error())
	}
	return tx.Hash, nil
}
