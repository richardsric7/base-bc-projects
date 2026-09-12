// Package rates abstracts exchange-rate lookups behind a small interface so
// the rates component doesn't care whether a number came from a fixture, a
// database table, or a live HTTP provider.
package rates

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

// Provider looks up an exchange rate between two currency/asset codes (e.g.
// base="USD", quote="ETH").
type Provider interface {
	GetRate(ctx context.Context, base, quote string) (decimal.Decimal, error)
}

// StaticProvider returns fixed rates from an in-memory table - useful for
// local development, tests, and as a template for a real fixture-backed or
// HTTP-backed provider.
type StaticProvider struct {
	rates map[string]decimal.Decimal
}

// NewStaticProvider builds a StaticProvider from a "BASE/QUOTE" -> rate map.
func NewStaticProvider(seed map[string]decimal.Decimal) *StaticProvider {
	if seed == nil {
		seed = map[string]decimal.Decimal{}
	}
	return &StaticProvider{rates: seed}
}

func (p *StaticProvider) GetRate(_ context.Context, base, quote string) (decimal.Decimal, error) {
	key := base + "/" + quote
	if rate, ok := p.rates[key]; ok {
		return rate, nil
	}
	if base == quote {
		return decimal.NewFromInt(1), nil
	}
	return decimal.Zero, fmt.Errorf("no rate configured for %s", key)
}
