package network

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"wallet-backend/internal/safe"
)

// SafeNonce reads a Gnosis Safe's current on-chain transaction nonce via
// Safe.nonce() - see safe.EncodeNonceCalldata's doc comment for why this is
// needed (fixing the nonce a proposed action's SafeTxHash is computed
// against) and the race this naive read defers to PLAN.md §13.10 Phase 6.
func (c *Client) SafeNonce(ctx context.Context, safeAddress string) (*big.Int, error) {
	addr := common.HexToAddress(safeAddress)
	data, err := safe.EncodeNonceCalldata()
	if err != nil {
		return nil, fmt.Errorf("encode nonce call: %w", err)
	}
	result, err := c.Eth.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("call nonce: %w", err)
	}
	return safe.DecodeNonceResult(result)
}

// receiptPollInterval is how often WaitForReceipt re-checks for a
// transaction's receipt while it hasn't landed yet - short enough to add
// negligible latency on Base's ~2-second blocks without hammering the RPC
// endpoint.
const receiptPollInterval = 2 * time.Second

// WaitForReceipt blocks until txHash's receipt is available or ctx is
// done, returning whether the transaction succeeded (receipt.Status == 1).
// Used by the shared-access relayer pool (PLAN.md §13.12) to know exactly
// when a relayer may be released back to the pool - not the moment a
// submission is merely broadcast, which is the race the original's own
// channel-account pool was found to have (PLAN.md §13.12's audit).
func (c *Client) WaitForReceipt(ctx context.Context, txHash string) (bool, error) {
	hash := common.HexToHash(txHash)
	for {
		receipt, err := c.Eth.TransactionReceipt(ctx, hash)
		if err == nil {
			return receipt.Status == types.ReceiptStatusSuccessful, nil
		}
		if !errors.Is(err, ethereum.NotFound) {
			return false, fmt.Errorf("fetch receipt: %w", err)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(receiptPollInterval):
		}
	}
}
