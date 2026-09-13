package network

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// transferEventTopic and approvalEventTopic are the keccak256 signature
// hashes ERC-20 contracts emit as topic[0] on Transfer(address,address,
// uint256) and Approval(address,address,uint256) - the two event types
// PLAN.md §2 identifies as the Base substitute for Horizon operation
// streaming's payment/trustline/offer feed.
var transferEventTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
var approvalEventTopic = crypto.Keccak256Hash([]byte("Approval(address,address,uint256)"))

// AddressWatcher polls eth_getLogs over the block range since its last poll
// for Transfer/Approval events touching a set of addresses callers have
// registered interest in, and fires a one-shot callback for each address an
// event touches. It replaces Horizon's operation/effect streaming, which
// this codebase used only to invalidate caches when something changed for
// an account - see PLAN.md §2's "Horizon operation streaming" row for why
// polling recent blocks is the direct Base equivalent, and §5 for why this
// lives in internal/network rather than a new package (this is the only
// place allowed to talk to go-ethereum/ethclient directly).
//
// A registration fires at most once: once an address's callbacks run, they
// are removed. This matches its intended use (invalidate a cache entry,
// then re-register only once something re-populates it) rather than
// firing repeatedly for every subsequent log touching the same address.
type AddressWatcher struct {
	client *Client

	mu        sync.Mutex
	fromBlock uint64
	callbacks map[common.Address][]func()
}

// NewAddressWatcher constructs a watcher with nothing registered yet. The
// first Poll call seeds its block cursor at the chain's current head, so no
// backlog of historical logs is processed on startup.
func NewAddressWatcher(client *Client) *AddressWatcher {
	return &AddressWatcher{client: client, callbacks: make(map[common.Address][]func())}
}

// Watch registers onEvent to run the next time a Transfer or Approval log
// names address as either the sender or the recipient/spender. Safe to call
// from multiple goroutines and multiple times for the same address (e.g.
// once per cached response that depends on it).
func (w *AddressWatcher) Watch(address string, onEvent func()) {
	addr := common.HexToAddress(address)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.callbacks[addr] = append(w.callbacks[addr], onEvent)
}

// Poll runs one polling pass: it fetches the chain's current block height,
// filters logs since the last pass, and fires (then clears) the callbacks
// registered for any address a matching log touches. Call it on a timer -
// see main.go, which runs it roughly every two Base blocks per PLAN.md §2.
func (w *AddressWatcher) Poll(ctx context.Context) error {
	w.mu.Lock()
	empty := len(w.callbacks) == 0
	w.mu.Unlock()
	if empty {
		return nil
	}

	latest, err := w.client.Eth.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get latest block number: %w", err)
	}

	w.mu.Lock()
	if w.fromBlock == 0 {
		// First pass: start from the current head rather than genesis, so
		// startup never triggers a chain-wide log scan.
		w.fromBlock = latest
	}
	fromBlock := w.fromBlock
	w.mu.Unlock()
	if latest < fromBlock {
		return nil
	}

	logs, err := w.client.Eth.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(latest),
		Topics:    [][]common.Hash{{transferEventTopic, approvalEventTopic}},
	})
	if err != nil {
		return fmt.Errorf("filter logs: %w", err)
	}

	w.mu.Lock()
	w.fromBlock = latest + 1
	w.mu.Unlock()

	w.fireCallbacksFor(extractTouchedAddresses(logs))
	return nil
}

// extractTouchedAddresses reads the indexed "from"/"to" (or "owner"/
// "spender") addresses out of a batch of Transfer/Approval logs. Both event
// types encode their two address arguments as topics[1] and topics[2] (an
// address left-padded to 32 bytes), so this needs no per-event-type
// branching. Pulled out as a pure function so it can be unit-tested without
// a live or simulated chain.
func extractTouchedAddresses(logs []types.Log) []common.Address {
	seen := make(map[common.Address]bool)
	var touched []common.Address
	for _, l := range logs {
		for _, topicIndex := range []int{1, 2} {
			if len(l.Topics) <= topicIndex {
				continue
			}
			addr := common.BytesToAddress(l.Topics[topicIndex].Bytes())
			if !seen[addr] {
				seen[addr] = true
				touched = append(touched, addr)
			}
		}
	}
	return touched
}

// fireCallbacksFor runs and clears every callback registered for each given
// address. Pulled out from Poll so the registration/firing bookkeeping is
// unit-testable independent of FilterLogs/BlockNumber.
func (w *AddressWatcher) fireCallbacksFor(touched []common.Address) {
	w.mu.Lock()
	var fire []func()
	for _, addr := range touched {
		if cbs, ok := w.callbacks[addr]; ok {
			fire = append(fire, cbs...)
			delete(w.callbacks, addr)
		}
	}
	w.mu.Unlock()

	for _, cb := range fire {
		cb()
	}
}
