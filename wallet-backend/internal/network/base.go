// Package network is the single point of contact with the Base blockchain
// (an OP-Stack Ethereum L2). Every other package that needs to read chain
// state or move a transaction through the network goes through the Client
// defined here - nothing else in the codebase should import
// go-ethereum/ethclient directly. That keeps a future chain swap (or a
// second chain) to a one-package change.
//
// Wallets in this template are non-custodial: the server only ever sees a
// user's EVM address. Any operation that changes a user's own account (a
// payment, an approval, a swap) is a two-step "build then submit" flow -
// BuildXXX returns an unsigned transaction for the client to sign with the
// key it alone holds, and SubmitSignedTransaction takes the signed result
// back. The server never asks for, stores, or signs with a user's private
// key. See PLAN.md §3 for why Build returns everything needed to sign
// completely offline (nonce, gas, chain ID) rather than requiring another
// round trip at signing time.
package network

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client wraps a JSON-RPC connection to a Base (or any EVM) node.
type Client struct {
	Eth     *ethclient.Client
	ChainID *big.Int
}

// NewClient dials rpcURL and wraps it for use against the given chain ID
// (8453 for Base Mainnet, 84532 for Base Sepolia - see PLAN.md §8).
// chainID is taken from config rather than queried from the node so a
// misconfigured RPC endpoint fails loudly instead of silently signing
// transactions for the wrong network.
func NewClient(ctx context.Context, rpcURL string, chainID int64) (*Client, error) {
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial base rpc %q: %w", rpcURL, err)
	}
	return &Client{Eth: eth, ChainID: big.NewInt(chainID)}, nil
}

// UnsignedTx is everything an offline signer needs to construct and sign an
// EIP-1559 transaction with no further network access - see PLAN.md §3.
// Every numeric field is a 0x-prefixed hex string so the payload round-trips
// through JSON without precision loss, matching what ethers.js/viem-style
// libraries already expect from a populated transaction request.
type UnsignedTx struct {
	ChainID              string `json:"chainId"`
	Nonce                uint64 `json:"nonce"`
	To                   string `json:"to,omitempty"` // omitted for contract creation, unused by this template
	Value                string `json:"value"`
	Data                 string `json:"data"`
	Gas                  uint64 `json:"gas"`
	MaxFeePerGas         string `json:"maxFeePerGas"`
	MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas"`
	Type                 string `json:"type"` // always "0x2" (EIP-1559) in this template
}

// buildUnsignedTx resolves nonce and EIP-1559 gas parameters against current
// chain state and estimates a gas limit for the given call. explicitNonce
// lets a caller pre-book a specific nonce (see PLAN.md §3's offline-batching
// requirement); nil means "use the next pending nonce."
func (c *Client) buildUnsignedTx(ctx context.Context, from common.Address, to *common.Address, value *big.Int, data []byte, explicitNonce *uint64) (*UnsignedTx, error) {
	var nonce uint64
	var err error
	if explicitNonce != nil {
		nonce = *explicitNonce
	} else {
		nonce, err = c.Eth.PendingNonceAt(ctx, from)
		if err != nil {
			return nil, fmt.Errorf("fetch nonce: %w", err)
		}
	}

	tipCap, err := c.Eth.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest gas tip cap: %w", err)
	}

	header, err := c.Eth.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch latest header: %w", err)
	}
	baseFee := header.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	// 2x the current base fee plus the tip covers several blocks of base-fee
	// growth, matching the headroom most wallet libraries apply by default.
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tipCap)

	gasLimit, err := c.Eth.EstimateGas(ctx, ethereum.CallMsg{From: from, To: to, Value: value, Data: data})
	if err != nil {
		return nil, fmt.Errorf("estimate gas: %w", err)
	}
	gasLimit = gasLimit * 12 / 10 // 20% headroom over the point estimate

	var toStr string
	if to != nil {
		toStr = to.Hex()
	}
	if value == nil {
		value = big.NewInt(0)
	}

	return &UnsignedTx{
		ChainID:              hexutil.EncodeBig(c.ChainID),
		Nonce:                nonce,
		To:                   toStr,
		Value:                hexutil.EncodeBig(value),
		Data:                 hexutil.Encode(data),
		Gas:                  gasLimit,
		MaxFeePerGas:         hexutil.EncodeBig(feeCap),
		MaxPriorityFeePerGas: hexutil.EncodeBig(tipCap),
		Type:                 "0x2",
	}, nil
}

// BuildNativeTransferTx builds an unsigned ETH transfer.
func (c *Client) BuildNativeTransferTx(ctx context.Context, from, to string, amountWei *big.Int, explicitNonce *uint64) (*UnsignedTx, error) {
	toAddr := common.HexToAddress(to)
	return c.buildUnsignedTx(ctx, common.HexToAddress(from), &toAddr, amountWei, nil, explicitNonce)
}

// BuildERC20TransferTx builds an unsigned ERC-20 transfer(to, amount) call.
func (c *Client) BuildERC20TransferTx(ctx context.Context, from, tokenAddress, to string, amount *big.Int, explicitNonce *uint64) (*UnsignedTx, error) {
	data, err := erc20ABI.Pack("transfer", common.HexToAddress(to), amount)
	if err != nil {
		return nil, fmt.Errorf("encode erc20 transfer: %w", err)
	}
	tokenAddr := common.HexToAddress(tokenAddress)
	return c.buildUnsignedTx(ctx, common.HexToAddress(from), &tokenAddr, big.NewInt(0), data, explicitNonce)
}

// BuildApproveTx builds an unsigned ERC-20 approve(spender, amount) call -
// the base template's substitute for Stellar's trustline, see PLAN.md §5.1.
// Callers should prefer an exact amount over an unlimited allowance and
// approve(0) once the spender has consumed it, to avoid leaving a standing
// permission wider than the operation that needed it.
func (c *Client) BuildApproveTx(ctx context.Context, owner, tokenAddress, spender string, amount *big.Int, explicitNonce *uint64) (*UnsignedTx, error) {
	data, err := erc20ABI.Pack("approve", common.HexToAddress(spender), amount)
	if err != nil {
		return nil, fmt.Errorf("encode erc20 approve: %w", err)
	}
	tokenAddr := common.HexToAddress(tokenAddress)
	return c.buildUnsignedTx(ctx, common.HexToAddress(owner), &tokenAddr, big.NewInt(0), data, explicitNonce)
}

// BuildContractCallTx builds an unsigned call to an arbitrary contract with
// pre-encoded calldata. This is the generic primitive the swaps component
// uses to call whatever DEX router is configured (see PLAN.md §2 and §10 -
// deliberately not hardcoded to one router's ABI).
func (c *Client) BuildContractCallTx(ctx context.Context, from, contractAddress string, value *big.Int, data []byte, explicitNonce *uint64) (*UnsignedTx, error) {
	contractAddr := common.HexToAddress(contractAddress)
	return c.buildUnsignedTx(ctx, common.HexToAddress(from), &contractAddr, value, data, explicitNonce)
}

// SubmitSignedTransaction decodes a client-signed raw transaction (0x-prefixed
// RLP, exactly what any offline signer produces) and submits it to the
// network, returning its hash.
func (c *Client) SubmitSignedTransaction(ctx context.Context, rawTxHex string) (string, error) {
	raw, err := hexutil.Decode(rawTxHex)
	if err != nil {
		return "", fmt.Errorf("decode raw transaction: %w", err)
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return "", fmt.Errorf("parse raw transaction: %w", err)
	}
	if err := c.Eth.SendTransaction(ctx, tx); err != nil {
		return "", fmt.Errorf("submit transaction: %w", err)
	}
	return tx.Hash().Hex(), nil
}

// NativeBalance returns an address's ETH balance in wei.
func (c *Client) NativeBalance(ctx context.Context, address string) (*big.Int, error) {
	return c.Eth.BalanceAt(ctx, common.HexToAddress(address), nil)
}

// ERC20BalanceOf reads a token balance via balanceOf(owner).
func (c *Client) ERC20BalanceOf(ctx context.Context, tokenAddress, owner string) (*big.Int, error) {
	data, err := erc20ABI.Pack("balanceOf", common.HexToAddress(owner))
	if err != nil {
		return nil, fmt.Errorf("encode balanceOf: %w", err)
	}
	tokenAddr := common.HexToAddress(tokenAddress)
	result, err := c.Eth.CallContract(ctx, ethereum.CallMsg{To: &tokenAddr, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("call balanceOf: %w", err)
	}
	balance := new(big.Int)
	if err := erc20ABI.UnpackIntoInterface(&balance, "balanceOf", result); err != nil {
		return nil, fmt.Errorf("decode balanceOf result: %w", err)
	}
	return balance, nil
}

// ERC20Allowance reads the remaining allowance a spender has over an owner's tokens.
func (c *Client) ERC20Allowance(ctx context.Context, tokenAddress, owner, spender string) (*big.Int, error) {
	data, err := erc20ABI.Pack("allowance", common.HexToAddress(owner), common.HexToAddress(spender))
	if err != nil {
		return nil, fmt.Errorf("encode allowance: %w", err)
	}
	tokenAddr := common.HexToAddress(tokenAddress)
	result, err := c.Eth.CallContract(ctx, ethereum.CallMsg{To: &tokenAddr, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("call allowance: %w", err)
	}
	allowance := new(big.Int)
	if err := erc20ABI.UnpackIntoInterface(&allowance, "allowance", result); err != nil {
		return nil, fmt.Errorf("decode allowance result: %w", err)
	}
	return allowance, nil
}

const erc20ABIJSON = `[
	{"constant":false,"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"},
	{"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"type":"function"},
	{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"constant":true,"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"name":"allowance","outputs":[{"name":"","type":"uint256"}],"type":"function"}
]`

var erc20ABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(erc20ABIJSON))
	if err != nil {
		panic("network: invalid embedded ERC-20 ABI: " + err.Error())
	}
	erc20ABI = parsed
}
