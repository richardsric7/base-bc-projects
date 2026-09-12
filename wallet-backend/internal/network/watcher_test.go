package network

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// addressTopic left-pads an address into a 32-byte topic, matching how the
// EVM encodes an indexed address event argument.
func addressTopic(address string) common.Hash {
	return common.BytesToHash(common.HexToAddress(address).Bytes())
}

func TestExtractTouchedAddresses_TransferLog(t *testing.T) {
	from := "0x000000000000000000000000000000000000aa"
	to := "0x000000000000000000000000000000000000bb"
	logs := []types.Log{
		{Topics: []common.Hash{transferEventTopic, addressTopic(from), addressTopic(to)}},
	}

	touched := extractTouchedAddresses(logs)
	if len(touched) != 2 {
		t.Fatalf("expected 2 touched addresses, got %d: %+v", len(touched), touched)
	}
	if touched[0] != common.HexToAddress(from) || touched[1] != common.HexToAddress(to) {
		t.Fatalf("unexpected touched addresses: %+v", touched)
	}
}

func TestExtractTouchedAddresses_Dedupes(t *testing.T) {
	addr := "0x000000000000000000000000000000000000cc"
	logs := []types.Log{
		{Topics: []common.Hash{transferEventTopic, addressTopic(addr), addressTopic(addr)}},
		{Topics: []common.Hash{approvalEventTopic, addressTopic(addr), addressTopic("0x000000000000000000000000000000000000dd")}},
	}

	touched := extractTouchedAddresses(logs)
	if len(touched) != 2 {
		t.Fatalf("expected 2 distinct touched addresses, got %d: %+v", len(touched), touched)
	}
}

func TestExtractTouchedAddresses_IgnoresLogsMissingTopics(t *testing.T) {
	logs := []types.Log{
		{Topics: []common.Hash{transferEventTopic}},
	}
	touched := extractTouchedAddresses(logs)
	if len(touched) != 0 {
		t.Fatalf("expected no touched addresses for a topic-less log, got %+v", touched)
	}
}

func TestAddressWatcher_FiresRegisteredCallbackOnce(t *testing.T) {
	w := NewAddressWatcher(nil)
	addr := "0x000000000000000000000000000000000000ee"

	calls := 0
	w.Watch(addr, func() { calls++ })

	w.fireCallbacksFor([]common.Address{common.HexToAddress(addr)})
	if calls != 1 {
		t.Fatalf("expected callback to fire once, fired %d times", calls)
	}

	// A registration is one-shot: firing again for the same address (with
	// nothing re-registered) must not call it a second time.
	w.fireCallbacksFor([]common.Address{common.HexToAddress(addr)})
	if calls != 1 {
		t.Fatalf("expected callback not to fire again after being cleared, fired %d times", calls)
	}
}

func TestAddressWatcher_FiresAllCallbacksForSameAddress(t *testing.T) {
	w := NewAddressWatcher(nil)
	addr := "0x000000000000000000000000000000000000ff"

	firstCalled, secondCalled := false, false
	w.Watch(addr, func() { firstCalled = true })
	w.Watch(addr, func() { secondCalled = true })

	w.fireCallbacksFor([]common.Address{common.HexToAddress(addr)})
	if !firstCalled || !secondCalled {
		t.Fatalf("expected both registered callbacks to fire: first=%v second=%v", firstCalled, secondCalled)
	}
}

func TestAddressWatcher_IgnoresUnwatchedAddresses(t *testing.T) {
	w := NewAddressWatcher(nil)
	watched := "0x0000000000000000000000000000000000aaaa"
	unrelated := "0x0000000000000000000000000000000000bbbb"

	called := false
	w.Watch(watched, func() { called = true })

	w.fireCallbacksFor([]common.Address{common.HexToAddress(unrelated)})
	if called {
		t.Fatal("expected callback for a different address not to fire")
	}
}
