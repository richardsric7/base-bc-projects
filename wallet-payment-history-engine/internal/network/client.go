// Package network is this engine's single point of contact with Base. It
// deliberately does not reuse wallet-backend's own internal/network
// package (a cross-module import between two independent deployables would
// couple their release cycles) but follows the same shape: one Client
// wrapping ethclient, nothing else in this engine talks to go-ethereum
// directly.
package network

import (
	"context"
	"fmt"
	"math/big"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TransferEventTopic is the keccak256 signature hash of ERC-20's
// Transfer(address,address,uint256) event - the log topic every curated
// token emits on every transfer, mint (from = zero address), and burn
// (to = zero address). See PLAN.md §2/§3.3.
var TransferEventTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// ZeroAddress is the EVM mint/burn sentinel (PLAN.md §2, §3.3).
var ZeroAddress = common.Address{}

// Client wraps a JSON-RPC connection to a Base (or any EVM) node.
type Client struct {
	Eth     *ethclient.Client
	ChainID *big.Int
}

// NewClient dials rpcURL and wraps it for use against the given chain ID.
func NewClient(ctx context.Context, rpcURL string, chainID int64) (*Client, error) {
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial base rpc %q: %w", rpcURL, err)
	}
	return &Client{Eth: eth, ChainID: big.NewInt(chainID)}, nil
}

// BlockNumber returns the chain's current head block number.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.Eth.BlockNumber(ctx)
}

// TransferLogs fetches every Transfer event emitted by any of
// tokenAddresses over [fromBlock, toBlock] (inclusive). Restricting to the
// curated-token contract list (rather than every contract on the network,
// as the original Stellar engine's global unscoped stream did) keeps this
// a bounded, address-specific query instead of network-wide noise - see
// PLAN.md §3.2.
func (c *Client) TransferLogs(ctx context.Context, fromBlock, toBlock uint64, tokenAddresses []common.Address) ([]types.Log, error) {
	if len(tokenAddresses) == 0 {
		return nil, nil
	}
	return c.Eth.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: tokenAddresses,
		Topics:    [][]common.Hash{{TransferEventTopic}},
	})
}

// trackedAddressBatchSize bounds how many addresses go into a single
// topic-filter position per eth_getLogs call, keeping each request under
// RPC providers' typical topic-array size limits (PLAN.md §3.2's "chunked
// into address-batches" note).
const trackedAddressBatchSize = 200

// TransferLogsTouchingWallets fetches every Transfer event, emitted by any
// of tokenAddresses over [fromBlock, toBlock], whose from OR to is one of
// trackedAddresses. eth_getLogs ORs within a single topic position but ANDs
// across positions, so "from in set OR to in set" needs two queries (one
// per position) merged and deduplicated - a single query can't express an
// OR across topic1/topic2.
func (c *Client) TransferLogsTouchingWallets(ctx context.Context, fromBlock, toBlock uint64, tokenAddresses, trackedAddresses []common.Address) ([]types.Log, error) {
	if len(tokenAddresses) == 0 || len(trackedAddresses) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool)
	var merged []types.Log

	for start := 0; start < len(trackedAddresses); start += trackedAddressBatchSize {
		end := start + trackedAddressBatchSize
		if end > len(trackedAddresses) {
			end = len(trackedAddresses)
		}
		batch := addressesToTopicHashes(trackedAddresses[start:end])

		fromLogs, err := c.Eth.FilterLogs(ctx, ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(fromBlock),
			ToBlock:   new(big.Int).SetUint64(toBlock),
			Addresses: tokenAddresses,
			Topics:    [][]common.Hash{{TransferEventTopic}, batch},
		})
		if err != nil {
			return nil, fmt.Errorf("filter transfer logs (from-side, batch %d): %w", start, err)
		}
		toLogs, err := c.Eth.FilterLogs(ctx, ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(fromBlock),
			ToBlock:   new(big.Int).SetUint64(toBlock),
			Addresses: tokenAddresses,
			Topics:    [][]common.Hash{{TransferEventTopic}, nil, batch},
		})
		if err != nil {
			return nil, fmt.Errorf("filter transfer logs (to-side, batch %d): %w", start, err)
		}

		for _, l := range append(fromLogs, toLogs...) {
			key := fmt.Sprintf("%s:%d", l.TxHash.Hex(), l.Index)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, l)
		}
	}

	return merged, nil
}

// addressesToTopicHashes left-pads each address to a 32-byte topic value,
// the encoding an indexed `address` event argument uses in a log topic.
func addressesToTopicHashes(addrs []common.Address) []common.Hash {
	hashes := make([]common.Hash, len(addrs))
	for i, a := range addrs {
		hashes[i] = common.BytesToHash(a.Bytes())
	}
	return hashes
}

// DecodeTransfer reads the from/to/value out of a Transfer log. Transfer's
// two address arguments are indexed (topics[1]/topics[2], each an address
// left-padded to 32 bytes); its value is the sole non-indexed argument in
// Data.
func DecodeTransfer(l types.Log) (from, to common.Address, value *big.Int, err error) {
	if len(l.Topics) < 3 {
		return common.Address{}, common.Address{}, nil, fmt.Errorf("transfer log has %d topics, want 3", len(l.Topics))
	}
	if len(l.Data) < 32 {
		return common.Address{}, common.Address{}, nil, fmt.Errorf("transfer log data too short: %d bytes", len(l.Data))
	}
	from = common.BytesToAddress(l.Topics[1].Bytes())
	to = common.BytesToAddress(l.Topics[2].Bytes())
	value = new(big.Int).SetBytes(l.Data[:32])
	return from, to, value, nil
}

// BlockWithNativeTransfers returns every transaction in blockNumber whose
// value is non-zero, alongside the block's own timestamp - the native-ETH
// counterpart to TransferLogs, since a plain ETH send emits no log at all
// (PLAN.md §2's "no equivalent — dropped" row for create_account is the
// same underlying reason: EVM value movement isn't always observable via
// logs).
func (c *Client) BlockWithNativeTransfers(ctx context.Context, blockNumber uint64) (transactions []*types.Transaction, blockTime uint64, err error) {
	block, err := c.Eth.BlockByNumber(ctx, new(big.Int).SetUint64(blockNumber))
	if err != nil {
		return nil, 0, fmt.Errorf("fetch block %d: %w", blockNumber, err)
	}
	var withValue []*types.Transaction
	for _, tx := range block.Transactions() {
		if tx.Value() != nil && tx.Value().Sign() > 0 {
			withValue = append(withValue, tx)
		}
	}
	return withValue, block.Time(), nil
}

// TransactionSender recovers a transaction's sender address - needed for
// native-value transfers, since a *types.Transaction only carries its
// signature, not a plain "from" field.
func (c *Client) TransactionSender(tx *types.Transaction) (common.Address, error) {
	signer := types.LatestSignerForChainID(c.ChainID)
	return types.Sender(signer, tx)
}
