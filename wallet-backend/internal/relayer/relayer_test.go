package relayer

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestNewPool_DerivesDistinctAddressesDeterministically(t *testing.T) {
	a, err := NewPool("test-salt", 3)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	b, err := NewPool("test-salt", 3)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	addrsA, addrsB := a.Addresses(), b.Addresses()
	if len(addrsA) != 3 {
		t.Fatalf("expected 3 addresses, got %d", len(addrsA))
	}
	for i := range addrsA {
		if addrsA[i] != addrsB[i] {
			t.Fatalf("same salt produced different addresses at index %d: %s vs %s", i, addrsA[i], addrsB[i])
		}
	}
	seen := map[string]bool{}
	for _, a := range addrsA {
		if seen[a.Hex()] {
			t.Fatalf("duplicate relayer address %s", a.Hex())
		}
		seen[a.Hex()] = true
	}

	other, err := NewPool("different-salt", 3)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	otherAddrs := other.Addresses()
	for i := range addrsA {
		if addrsA[i] == otherAddrs[i] {
			t.Fatalf("different salts produced the same address at index %d", i)
		}
	}
}

func TestNewPool_ClampsSizeToAtLeastOne(t *testing.T) {
	p, err := NewPool("salt", 0)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if len(p.Addresses()) != 1 {
		t.Fatalf("expected pool size clamped to 1, got %d", len(p.Addresses()))
	}
}

func TestClaimRelease_RoundTrips(t *testing.T) {
	p, err := NewPool("salt", 1)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	addr := p.Addresses()[0]

	key, err := p.Claim(context.Background())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if key == nil {
		t.Fatal("expected a non-nil key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Claim(ctx); err == nil {
		t.Fatal("expected Claim to block (and time out) with the pool's only relayer already claimed")
	}

	p.Release(addr)

	if _, err := p.Claim(context.Background()); err != nil {
		t.Fatalf("expected Claim to succeed after Release, got: %v", err)
	}
}

func TestReserveAtStartup_KeepsReservedAddressesUnavailable(t *testing.T) {
	p, err := NewPool("salt", 2)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	addrs := p.Addresses()
	p.ReserveAtStartup([]common.Address{addrs[0]})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	first, err := p.Claim(ctx)
	if err != nil {
		t.Fatalf("expected the unreserved relayer to be claimable: %v", err)
	}
	_ = first

	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if _, err := p.Claim(ctx2); err == nil {
		t.Fatal("expected no further relayer to be available - one is reserved, the other already claimed")
	}

	p.Release(addrs[0])
	if _, err := p.Claim(context.Background()); err != nil {
		t.Fatalf("expected the reserved relayer to become claimable after Release: %v", err)
	}
}
