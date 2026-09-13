package network

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"
)

// TestPoolPrice_OneToOne checks a pool priced at exactly 1 token1 per
// token0 with equal decimals: sqrtPriceX96 = 2^96 means sqrtPrice = 1, so
// price = 1^2 = 1.
func TestPoolPrice_OneToOne(t *testing.T) {
	sqrtPriceX96 := new(big.Int).Lsh(big.NewInt(1), 96)
	price := PoolPrice(sqrtPriceX96, 18, 18)
	if !price.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("expected a price of 1, got %s", price.String())
	}
}

// TestPoolPrice_KnownRatio checks a pool priced at 4 token1 per token0
// with equal decimals: sqrtPrice = 2 (since 2^2 = 4), so
// sqrtPriceX96 = 2 * 2^96.
func TestPoolPrice_KnownRatio(t *testing.T) {
	sqrtPriceX96 := new(big.Int).Lsh(big.NewInt(2), 96)
	price := PoolPrice(sqrtPriceX96, 18, 18)
	if !price.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("expected a price of 4, got %s", price.String())
	}
}

// TestPoolPrice_DecimalAdjustment checks that differing decimals shift
// the raw price by the expected power of ten - e.g. token0 with 6
// decimals (like USDC) and token1 with 18 decimals (like WETH), at a raw
// 1:1 sqrtPrice, should read as 10^(6-18) = 10^-12, not 1.
func TestPoolPrice_DecimalAdjustment(t *testing.T) {
	sqrtPriceX96 := new(big.Int).Lsh(big.NewInt(1), 96)
	price := PoolPrice(sqrtPriceX96, 6, 18)
	expected := decimal.New(1, -12)
	if !price.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected.String(), price.String())
	}
}

// TestPoolPrice_ReverseDecimalAdjustment checks the opposite decimals
// ordering shifts the price the other way.
func TestPoolPrice_ReverseDecimalAdjustment(t *testing.T) {
	sqrtPriceX96 := new(big.Int).Lsh(big.NewInt(1), 96)
	price := PoolPrice(sqrtPriceX96, 18, 6)
	expected := decimal.New(1, 12)
	if !price.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected.String(), price.String())
	}
}
