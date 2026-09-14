package safe

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/common"
)

// signatureLen is the fixed size of one packed signature slot in the blob
// Safe.checkNSignatures walks - always 65 bytes per owner regardless of
// which of the three signature kinds it holds; a contract signature's
// actual payload lives in the dynamic tail instead (see PackSignatures).
const signatureLen = 65

// Signature is one owner's contribution to a Safe transaction's combined
// signature blob. Exactly one of EOA or Contract must be set - use
// EOAPersonalSignSignature or ContractSignature to construct one rather
// than building this directly, so the two never end up set at once.
type Signature struct {
	Owner    common.Address
	eoaSig   []byte // 65 bytes, r||s||v with v in {27,28}; nil for a contract signature
	contract []byte // nested EIP-1271 signature bytes; nil for an EOA signature
}

// EOAPersonalSignSignature wraps a plain owner's raw personal_sign
// signature (65 bytes, r||s||v with v as 27/28 or 0/1 - both normalized,
// matching what cryptoutil.VerifyPersonalSignBytes accepts) of a Safe
// transaction's SafeTxHash. This codebase standardizes every off-chain
// approval, Safe transactions included, on personal_sign rather than
// eth_signTypedData_v4 (PLAN.md §12) so that a single client-side signing
// primitive covers both - PackSignatures rewrites v to the 31/32 encoding
// Safe.checkNSignatures requires for this signing style (see its "v > 30"
// branch), so callers never have to think about that adjustment.
func EOAPersonalSignSignature(owner common.Address, rawSig []byte) (Signature, error) {
	if len(rawSig) != signatureLen {
		return Signature{}, fmt.Errorf("signature must be 65 bytes, got %d", len(rawSig))
	}
	sig := make([]byte, signatureLen)
	copy(sig, rawSig)
	if sig[64] < 27 {
		sig[64] += 27
	}
	if sig[64] != 27 && sig[64] != 28 {
		return Signature{}, fmt.Errorf("invalid signature recovery id %d", sig[64])
	}
	sig[64] += 4 // personal_sign encoding Safe.checkNSignatures expects: v in {31,32}
	return Signature{Owner: owner, eoaSig: sig}, nil
}

// ContractSignature wraps a nested Safe's (or any other EIP-1271
// contract's) own signature bytes for the EIP-1271 "contract signature"
// slot Safe.checkNSignatures recognizes (v == 0, r == the contract's
// address). data is whatever that contract's own isValidSignature accepts
// as its second argument - for a nested Safe reached through
// CompatibilityFallbackHandler, that's itself a PackSignatures blob over
// the nested Safe's own owners, verified against
// safe.MessageHashForSafe(nestedDomainSeparator, outerPreImageBytes).
func ContractSignature(owner common.Address, data []byte) Signature {
	return Signature{Owner: owner, contract: data}
}

// PackSignatures builds the combined signatures argument
// Safe.execTransaction expects: a fixed-size 65-byte-per-owner static
// array (v/r/s triples for EOA signatures; v=0, r=owner address, s=byte
// offset into the tail for contract signatures), sorted by ascending
// owner address as Safe.checkNSignatures requires (it rejects any run
// where an owner's address does not strictly increase - PLAN.md §13's
// design note on why nested-ownership signature collection must sort
// before submission), followed by a dynamic tail holding each contract
// signature's own length-prefixed bytes in the same order.
//
// sigs must contain exactly the Safe's threshold-worth (or more) of
// signatures from distinct owners - this function does not deduplicate or
// validate owner membership itself, matching the layering the rest of
// this codebase's approval flows already use (a service layer collects
// and verifies individual signatures against known owners before calling
// this to assemble the final submission).
func PackSignatures(sigs []Signature) ([]byte, error) {
	if len(sigs) == 0 {
		return nil, fmt.Errorf("at least one signature is required")
	}
	sorted := make([]Signature, len(sigs))
	copy(sorted, sigs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Owner.Hex() < sorted[j].Owner.Hex()
	})
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Owner == sorted[i-1].Owner {
			return nil, fmt.Errorf("duplicate signature for owner %s", sorted[i].Owner.Hex())
		}
	}

	static := make([]byte, len(sorted)*signatureLen)
	var dynamic []byte
	dynamicOffset := len(static)

	for i, s := range sorted {
		slot := static[i*signatureLen : (i+1)*signatureLen]
		switch {
		case s.eoaSig != nil:
			copy(slot, s.eoaSig)
		case s.contract != nil:
			copy(slot[0:32], common.LeftPadBytes(s.Owner.Bytes(), 32))     // r = owner address, left-padded
			binary.BigEndian.PutUint64(slot[56:64], uint64(dynamicOffset)) // s = offset into the dynamic tail
			slot[64] = 0                                                   // v = 0: contract signature

			lengthPrefixed := make([]byte, 32+len(s.contract))
			binary.BigEndian.PutUint64(lengthPrefixed[24:32], uint64(len(s.contract)))
			copy(lengthPrefixed[32:], s.contract)
			dynamic = append(dynamic, lengthPrefixed...)
			dynamicOffset += len(lengthPrefixed)
		default:
			return nil, fmt.Errorf("signature for owner %s has neither an EOA nor a contract payload set", s.Owner.Hex())
		}
	}

	return append(static, dynamic...), nil
}
