package network

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"
)

// uniswapV3PoolABIJSON covers only the three read-only pool methods this
// package calls - a pool contract has many more, but nothing here writes
// to one or needs the rest. See PLAN.md §5: this is the assets
// order-book/price-discovery replacement for Stellar's protocol-level
// order book, added directly to internal/network per its own doc comment
// ("nothing else in the codebase should import go-ethereum/ethclient
// directly").
const uniswapV3PoolABIJSON = `[
	{"inputs":[],"name":"slot0","outputs":[{"name":"sqrtPriceX96","type":"uint160"},{"name":"tick","type":"int24"},{"name":"observationIndex","type":"uint16"},{"name":"observationCardinality","type":"uint16"},{"name":"observationCardinalityNext","type":"uint16"},{"name":"feeProtocol","type":"uint8"},{"name":"unlocked","type":"bool"}],"stateMutability":"view","type":"function"},
	{"inputs":[],"name":"token0","outputs":[{"name":"","type":"address"}],"stateMutability":"view","type":"function"},
	{"inputs":[],"name":"token1","outputs":[{"name":"","type":"address"}],"stateMutability":"view","type":"function"}
]`

var uniswapV3PoolABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(uniswapV3PoolABIJSON))
	if err != nil {
		panic("network: invalid embedded Uniswap V3 pool ABI: " + err.Error())
	}
	uniswapV3PoolABI = parsed
}

// PoolState is a Uniswap V3 pool's current price state plus its two
// tokens' addresses (needed to know which side of the pair sqrtPriceX96's
// convention - token1 per token0 - refers to).
type PoolState struct {
	SqrtPriceX96 *big.Int
	Tick         int32
	Token0       common.Address
	Token1       common.Address
}

// GetPoolState reads a Uniswap V3 pool's slot0 plus its token0/token1
// addresses via three eth_call reads. Use PoolPrice to turn SqrtPriceX96
// into a human-readable price once you know both tokens' decimals (a
// CuratedToken lookup, typically).
func (c *Client) GetPoolState(ctx context.Context, poolAddress string) (*PoolState, error) {
	pool := common.HexToAddress(poolAddress)

	slot0Data, err := uniswapV3PoolABI.Pack("slot0")
	if err != nil {
		return nil, fmt.Errorf("encode slot0: %w", err)
	}
	slot0Result, err := c.Eth.CallContract(ctx, ethereum.CallMsg{To: &pool, Data: slot0Data}, nil)
	if err != nil {
		return nil, fmt.Errorf("call slot0: %w", err)
	}
	unpacked, err := uniswapV3PoolABI.Methods["slot0"].Outputs.Unpack(slot0Result)
	if err != nil {
		return nil, fmt.Errorf("decode slot0: %w", err)
	}
	sqrtPriceX96, ok := unpacked[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("decode slot0: unexpected sqrtPriceX96 type")
	}
	tick, ok := unpacked[1].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("decode slot0: unexpected tick type")
	}

	token0, err := c.callPoolAddress(ctx, pool, "token0")
	if err != nil {
		return nil, err
	}
	token1, err := c.callPoolAddress(ctx, pool, "token1")
	if err != nil {
		return nil, err
	}

	return &PoolState{
		SqrtPriceX96: sqrtPriceX96,
		Tick:         int32(tick.Int64()),
		Token0:       token0,
		Token1:       token1,
	}, nil
}

func (c *Client) callPoolAddress(ctx context.Context, pool common.Address, method string) (common.Address, error) {
	data, err := uniswapV3PoolABI.Pack(method)
	if err != nil {
		return common.Address{}, fmt.Errorf("encode %s: %w", method, err)
	}
	result, err := c.Eth.CallContract(ctx, ethereum.CallMsg{To: &pool, Data: data}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("call %s: %w", method, err)
	}
	unpacked, err := uniswapV3PoolABI.Methods[method].Outputs.Unpack(result)
	if err != nil {
		return common.Address{}, fmt.Errorf("decode %s: %w", method, err)
	}
	addr, ok := unpacked[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("decode %s: unexpected type", method)
	}
	return addr, nil
}

// sqrtPriceX96Scale is 2^96, the fixed-point scale Uniswap V3 encodes
// sqrtPriceX96 in.
var sqrtPriceX96Scale = decimal.NewFromBigInt(new(big.Int).Lsh(big.NewInt(1), 96), 0)

// PoolPrice converts a Uniswap V3 sqrtPriceX96 value into the human price
// of token1 per whole unit of token0 (Uniswap's own price convention -
// price = token1 raw units per token0 raw unit, squared from the pool's
// square-root fixed-point representation), adjusted for each token's own
// decimals. Pass decimals0/decimals1 in the same token0/token1 order
// GetPoolState's PoolState reports, or the result is inverted.
func PoolPrice(sqrtPriceX96 *big.Int, decimals0, decimals1 uint8) decimal.Decimal {
	sqrtPrice := decimal.NewFromBigInt(sqrtPriceX96, 0).Div(sqrtPriceX96Scale)
	rawPrice := sqrtPrice.Mul(sqrtPrice)
	// decimal.New(1, n) is exactly 10^n, including for negative n - no
	// separate reciprocal branch needed for decimals0 < decimals1.
	decimalAdjustment := decimal.New(1, int32(decimals0)-int32(decimals1))
	return rawPrice.Mul(decimalAdjustment)
}
