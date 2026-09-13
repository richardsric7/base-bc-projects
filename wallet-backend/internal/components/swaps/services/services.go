// Package services implements the swaps component as a generic,
// router-address-configurable contract-call builder rather than a
// hardcoded integration with one DEX - see PLAN.md §2 and §10. A caller
// supplies the router contract's address, its ABI (or the relevant
// fragment of it), the method to call, and its arguments; this package
// ABI-encodes the call and hands it to internal/network to build an
// unsigned transaction. This works unmodified against Uniswap V3's
// SwapRouter02, Aerodrome, or any other router deployed on Base - the
// project configures which one it wants, this code doesn't pick for it.
package services

import (
	"context"
	"math/big"

	"wallet-backend/internal/alerting"
	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/network"
	"wallet-backend/internal/validators"
)

type Service struct {
	Blockchain *network.Client
	// Alerts reports a rejected submission to an operational channel -
	// defaults to alerting.NoopNotifier (see New); main.go wires the real
	// one in post-construction. See PLAN.md §4.13.
	Alerts alerting.Notifier
}

func New(blockchain *network.Client) *Service {
	return &Service{Blockchain: blockchain, Alerts: alerting.NewNoopNotifier()}
}

// BuildSwapInput is everything needed to encode and build a swap-router call.
type BuildSwapInput struct {
	From          string        // the caller's address, from their verified session
	RouterAddress string        // the DEX router contract to call
	RouterABI     string        // JSON ABI (or just the relevant method fragment)
	Method        string        // e.g. "exactInputSingle" for Uniswap V3's SwapRouter02
	Args          []interface{} // decoded JSON args; see network.EncodeContractCall for supported types
	ValueWei      string        // ETH sent with the call, decimal string; "" or "0" for none (e.g. a token-to-token swap)
	Nonce         *uint64       // optional explicit nonce - see PLAN.md §3 on offline-batched nonces
}

// BuildSwapTx ABI-encodes the configured router call and returns an
// unsigned transaction for the caller to sign client-side.
func (s *Service) BuildSwapTx(ctx context.Context, input BuildSwapInput) (*network.UnsignedTx, error) {
	if !validators.IsValidAddress(input.From) {
		return nil, apperrors.BadRequest("invalid address")
	}
	if !validators.IsValidAddress(input.RouterAddress) {
		return nil, apperrors.BadRequest("invalid router contract address")
	}
	if input.Method == "" {
		return nil, apperrors.BadRequest("method is required")
	}

	value := big.NewInt(0)
	if input.ValueWei != "" {
		var ok bool
		value, ok = new(big.Int).SetString(input.ValueWei, 10)
		if !ok {
			return nil, apperrors.BadRequest("valueWei must be a decimal integer string")
		}
	}

	data, err := network.EncodeContractCall(input.RouterABI, input.Method, input.Args)
	if err != nil {
		return nil, apperrors.BadRequest("failed to encode router call: " + err.Error())
	}

	tx, err := s.Blockchain.BuildContractCallTx(ctx, input.From, input.RouterAddress, value, data, input.Nonce)
	if err != nil {
		return nil, apperrors.BadRequest(err.Error())
	}
	return tx, nil
}

// SubmitSwap submits a client-signed swap transaction and returns its hash.
func (s *Service) SubmitSwap(ctx context.Context, signedTx string) (string, error) {
	hash, err := s.Blockchain.SubmitSignedTransaction(ctx, signedTx)
	if err != nil {
		_ = s.Alerts.Notify("swap submission rejected by the network: " + err.Error())
		return "", apperrors.BadRequest("transaction rejected by the network: " + err.Error())
	}
	return hash, nil
}
