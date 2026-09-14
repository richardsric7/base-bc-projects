// Package relayer implements the pool of backend-operated Base EOAs that
// submit every Safe execTransaction call this codebase makes on a group's
// behalf, once its approval threshold is met. It directly mirrors the
// original Stellar port's channel-account pool - see PLAN.md §13.12 for
// the full audit of why: the original solves sequence-number contention
// across concurrent submissions from one account with a pool of dedicated
// accounts, claimed for the duration of one pending operation and released
// only once it resolves; Base's relayer EOAs have the exact same
// contention problem over their own Ethereum account nonce (§13.12's
// "risk 2" - distinct from "risk 1," a Safe's own internal nonce, which
// PLAN.md §13.10 Phase 6 closes separately with per-Safe reservation, not
// a pool).
//
// A relayer's private key is never stored anywhere: like every other
// server-controlled role in this codebase (faucet, escrow, treasury,
// issuer, distribution, Safe deployer), each one is deterministically
// derived from a shared salt via cryptoutil.DeriveKey, keyed by its index
// in the pool - fund the addresses NewPool reports (Addresses) with ETH
// once, and the pool is fully reconstructible from RELAYER_KEY_SALT alone
// on every restart.
package relayer

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"wallet-backend/internal/cryptoutil"
)

// Pool is a fixed-size set of relayer EOAs. A relayer is claimed for the
// entire lifetime of one on-chain submission - from just before it's
// broadcast until it's confirmed, failed, or abandoned - and released back
// to the pool only then, never merely on broadcast (PLAN.md §13.12's audit
// of why the original's own release-too-early bug matters).
type Pool struct {
	keys      map[common.Address]*ecdsa.PrivateKey
	addresses []common.Address // stable, sorted iteration order
	available chan common.Address
	mu        sync.Mutex
	inUse     map[common.Address]bool
}

// NewPool derives size relayer keys from keySalt (each
// cryptoutil.DeriveKey(keySalt + "|relayer|" + index), the same derived-key
// convention every other server-controlled key in this codebase uses) and
// returns a Pool with all of them immediately available. size is clamped
// to at least 1 - a pool of zero relayers could never submit anything.
func NewPool(keySalt string, size int) (*Pool, error) {
	if size < 1 {
		size = 1
	}
	keys := make(map[common.Address]*ecdsa.PrivateKey, size)
	addresses := make([]common.Address, 0, size)
	for i := 0; i < size; i++ {
		key, err := cryptoutil.DeriveKey(fmt.Sprintf("%s|relayer|%d", keySalt, i))
		if err != nil {
			return nil, fmt.Errorf("derive relayer key %d: %w", i, err)
		}
		addr := crypto.PubkeyToAddress(key.PublicKey)
		keys[addr] = key
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i].Hex() < addresses[j].Hex() })

	p := &Pool{
		keys:      keys,
		addresses: addresses,
		available: make(chan common.Address, size),
		inUse:     make(map[common.Address]bool, size),
	}
	for _, addr := range addresses {
		p.available <- addr
	}
	return p, nil
}

// Addresses returns every relayer address in the pool, in a stable order -
// use this to know which addresses to fund (DEPLOYMENT.md) and, at
// startup, which of them (if any) has an unconfirmed submission
// outstanding from before the last restart (see ReserveAtStartup).
func (p *Pool) Addresses() []common.Address {
	out := make([]common.Address, len(p.addresses))
	copy(out, p.addresses)
	return out
}

// ReserveAtStartup marks the given relayer addresses in-use before the
// pool ever serves a Claim call - the counterpart to the original's own
// startup-reconciliation goroutine, which re-marks a channel account
// in-use for any PendingAuth still PENDING at boot (PLAN.md §13.12): a
// relayer whose last known submission (per the database) hasn't been
// confirmed yet shouldn't be handed out for new work until that's
// resolved. addrs not recognized as pool members are ignored. Must be
// called once, before any concurrent Claim/Release traffic begins - it is
// not safe to call after the pool is already serving requests.
func (p *Pool) ReserveAtStartup(addrs []common.Address) {
	if len(addrs) == 0 {
		return
	}
	reserved := make(map[common.Address]bool, len(addrs))
	for _, a := range addrs {
		reserved[a] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.available)
	for i := 0; i < n; i++ {
		addr := <-p.available
		if reserved[addr] {
			p.inUse[addr] = true
		} else {
			p.available <- addr
		}
	}
}

// Claim blocks until a relayer is free (or ctx is done) and returns its
// key, marking it in-use. Release must be called exactly once for the
// returned key's address, only once the submission it was claimed for has
// reached a final state.
func (p *Pool) Claim(ctx context.Context) (*ecdsa.PrivateKey, error) {
	select {
	case addr := <-p.available:
		p.mu.Lock()
		p.inUse[addr] = true
		p.mu.Unlock()
		return p.keys[addr], nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Release returns a relayer to the pool, available for the next Claim.
// Releasing an address the pool doesn't recognize, or one that isn't
// currently in use, is a no-op.
func (p *Pool) Release(addr common.Address) {
	p.mu.Lock()
	if _, ok := p.keys[addr]; !ok || !p.inUse[addr] {
		p.mu.Unlock()
		return
	}
	delete(p.inUse, addr)
	p.mu.Unlock()
	p.available <- addr
}
