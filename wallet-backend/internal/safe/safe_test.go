package safe

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestTypehashesMatchSolidity recomputes each EIP-712 typehash from its
// exact Solidity signature string and asserts it matches the literal hex
// this package hardcodes, copied from Safe.sol and
// CompatibilityFallbackHandler.sol. This is the whole point of not trusting
// a bare hex transcription: a mismatch here means a typo, not a
// theoretical concern.
func TestTypehashesMatchSolidity(t *testing.T) {
	cases := []struct {
		name      string
		signature string
		want      common.Hash
	}{
		{"DOMAIN_SEPARATOR_TYPEHASH", "EIP712Domain(uint256 chainId,address verifyingContract)", domainSeparatorTypehash},
		{"SAFE_TX_TYPEHASH", "SafeTx(address to,uint256 value,bytes data,uint8 operation,uint256 safeTxGas,uint256 baseGas,uint256 gasPrice,address gasToken,address refundReceiver,uint256 nonce)", safeTxTypehash},
		{"SAFE_MSG_TYPEHASH", "SafeMessage(bytes message)", safeMessageTypehash},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := crypto.Keccak256Hash([]byte(c.signature))
			if got != c.want {
				t.Fatalf("keccak256(%q) = %s, want %s", c.signature, got.Hex(), c.want.Hex())
			}
		})
	}
}

// fixedOwner/fixedTo/fixedProxyAddress/fixedInitializer/fixedDomainSeparator/
// fixedSafeTxHash are an independent-implementation cross-check: computed
// once via a from-scratch Python re-implementation of the exact same
// createProxyWithNonce/domainSeparator/getTransactionHash formulas (using
// eth_abi/pycryptodome rather than this package or go-ethereum), so a bug
// shared between "how I read the Solidity" and "how I wrote the Go" is far
// less likely to also be present in that from-scratch second
// implementation. See PLAN.md §13's Phase 1 notes for how these were
// derived.
var (
	fixedOwner           = common.HexToAddress("0x1000000000000000000000000000000000000001")
	fixedTo              = common.HexToAddress("0x2000000000000000000000000000000000000002")
	fixedProxyAddress    = common.HexToAddress("0x206eC93Ee894b0739ef81aA79d422b30da6A7b32")
	fixedInitializer     = common.FromHex("0xb63e800d0000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000140000000000000000000000000fd0732dc9e303f09fcef3a7388ad10a83459ec99000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100000000000000000000000010000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000000")
	fixedDomainSeparator = common.HexToHash("0x96f7d34377970de28bef216eac9e002b2b5cd26a8a4f34109cf4a48fba79521f")
	fixedSafeTxHash      = common.HexToHash("0x45d5410136091bc5795769894e1c6818e02363abea6dd455d5c892f462f18b4a")
)

func TestEncodeSetupCalldata_MatchesIndependentComputation(t *testing.T) {
	got, err := EncodeSetupCalldata([]common.Address{fixedOwner}, big.NewInt(1))
	if err != nil {
		t.Fatalf("EncodeSetupCalldata: %v", err)
	}
	if !bytes.Equal(got, fixedInitializer) {
		t.Fatalf("initializer mismatch:\n got  %x\n want %x", got, fixedInitializer)
	}
}

func TestComputeProxyAddress_MatchesIndependentComputation(t *testing.T) {
	got := ComputeProxyAddress(SingletonAddress, fixedInitializer, big.NewInt(0))
	if got != fixedProxyAddress {
		t.Fatalf("ComputeProxyAddress = %s, want %s", got.Hex(), fixedProxyAddress.Hex())
	}
}

func TestComputeProxyAddress_ChangesWithInputs(t *testing.T) {
	base := ComputeProxyAddress(SingletonAddress, fixedInitializer, big.NewInt(0))

	if got := ComputeProxyAddress(SingletonAddress, fixedInitializer, big.NewInt(1)); got == base {
		t.Fatal("changing saltNonce did not change the computed address")
	}

	otherInitializer, err := EncodeSetupCalldata([]common.Address{fixedTo}, big.NewInt(1))
	if err != nil {
		t.Fatalf("EncodeSetupCalldata: %v", err)
	}
	if got := ComputeProxyAddress(SingletonAddress, otherInitializer, big.NewInt(0)); got == base {
		t.Fatal("changing the initializer (owners) did not change the computed address")
	}

	if got := ComputeProxyAddress(SingletonAddress, fixedInitializer, big.NewInt(0)); got != base {
		t.Fatal("ComputeProxyAddress is not deterministic for identical inputs")
	}
}

func TestDomainSeparator_MatchesIndependentComputation(t *testing.T) {
	got := DomainSeparator(big.NewInt(8453), fixedProxyAddress)
	if got != fixedDomainSeparator {
		t.Fatalf("DomainSeparator = %s, want %s", got.Hex(), fixedDomainSeparator.Hex())
	}
}

func TestSafeTxHash_MatchesIndependentComputation(t *testing.T) {
	tx := SafeTx{
		To:             fixedTo,
		Value:          big.NewInt(0),
		Data:           []byte{},
		Operation:      OperationCall,
		SafeTxGas:      big.NewInt(0),
		BaseGas:        big.NewInt(0),
		GasPrice:       big.NewInt(0),
		GasToken:       common.Address{},
		RefundReceiver: common.Address{},
		Nonce:          big.NewInt(0),
	}
	got := SafeTxHash(fixedDomainSeparator, tx)
	if got != fixedSafeTxHash {
		t.Fatalf("SafeTxHash = %s, want %s", got.Hex(), fixedSafeTxHash.Hex())
	}
}

func TestPackSignatures_SortsByOwnerAscending(t *testing.T) {
	low := common.HexToAddress("0x1111111111111111111111111111111111111111")
	high := common.HexToAddress("0x9999999999999999999999999999999999999999")

	rawSigHigh := make([]byte, 65)
	rawSigHigh[0] = 0xAA
	rawSigHigh[64] = 27
	sigHigh, err := EOAPersonalSignSignature(high, rawSigHigh)
	if err != nil {
		t.Fatalf("EOAPersonalSignSignature: %v", err)
	}
	rawSigLow := make([]byte, 65)
	rawSigLow[0] = 0xBB
	rawSigLow[64] = 27
	sigLow, err := EOAPersonalSignSignature(low, rawSigLow)
	if err != nil {
		t.Fatalf("EOAPersonalSignSignature: %v", err)
	}

	// Passed in descending order deliberately - PackSignatures must sort.
	packed, err := PackSignatures([]Signature{sigHigh, sigLow})
	if err != nil {
		t.Fatalf("PackSignatures: %v", err)
	}
	if len(packed) != 2*signatureLen {
		t.Fatalf("packed length = %d, want %d", len(packed), 2*signatureLen)
	}
	// Passed as [high, low]; PackSignatures must reorder so the lower
	// address's signature occupies the first slot.
	if !bytes.Equal(packed[0:signatureLen], sigLow.eoaSig) {
		t.Fatal("first packed slot is not the lower-address owner's signature - PackSignatures did not sort")
	}
	if !bytes.Equal(packed[signatureLen:2*signatureLen], sigHigh.eoaSig) {
		t.Fatal("second packed slot is not the higher-address owner's signature - PackSignatures did not sort")
	}
}

func TestEOAPersonalSignSignature_RewritesVTo31Or32(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	for _, v := range []byte{0, 1, 27, 28} {
		rawSig := make([]byte, 65)
		rawSig[64] = v
		sig, err := EOAPersonalSignSignature(owner, rawSig)
		if err != nil {
			t.Fatalf("EOAPersonalSignSignature(v=%d): %v", v, err)
		}
		if sig.eoaSig[64] != 31 && sig.eoaSig[64] != 32 {
			t.Fatalf("EOAPersonalSignSignature(v=%d) produced v=%d, want 31 or 32", v, sig.eoaSig[64])
		}
	}
}

func TestPackSignatures_RejectsDuplicateOwner(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	rawSig := make([]byte, 65)
	rawSig[64] = 27
	sig, err := EOAPersonalSignSignature(owner, rawSig)
	if err != nil {
		t.Fatalf("EOAPersonalSignSignature: %v", err)
	}
	if _, err := PackSignatures([]Signature{sig, sig}); err == nil {
		t.Fatal("expected an error for a duplicate owner, got nil")
	}
}

func TestPackSignatures_ContractSignatureLayout(t *testing.T) {
	eoaOwner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	contractOwner := common.HexToAddress("0x9999999999999999999999999999999999999999")
	nestedPayload := []byte("nested-safe-signature-blob")

	rawSig := make([]byte, 65)
	rawSig[64] = 27
	eoaSig, err := EOAPersonalSignSignature(eoaOwner, rawSig)
	if err != nil {
		t.Fatalf("EOAPersonalSignSignature: %v", err)
	}

	packed, err := PackSignatures([]Signature{eoaSig, ContractSignature(contractOwner, nestedPayload)})
	if err != nil {
		t.Fatalf("PackSignatures: %v", err)
	}

	wantStaticLen := 2 * signatureLen
	if len(packed) != wantStaticLen+32+len(nestedPayload) {
		t.Fatalf("packed length = %d, want %d", len(packed), wantStaticLen+32+len(nestedPayload))
	}

	// Second slot (contractOwner sorts after eoaOwner) must be v=0 with r
	// equal to the owner address and s pointing past the static array.
	secondSlot := packed[signatureLen : 2*signatureLen]
	if secondSlot[64] != 0 {
		t.Fatalf("contract signature v = %d, want 0", secondSlot[64])
	}
	gotOwner := common.BytesToAddress(secondSlot[0:32])
	if gotOwner != contractOwner {
		t.Fatalf("contract signature r decodes to owner %s, want %s", gotOwner.Hex(), contractOwner.Hex())
	}
	offset := new(big.Int).SetBytes(secondSlot[32:64]).Uint64()
	if offset != uint64(wantStaticLen) {
		t.Fatalf("contract signature s (offset) = %d, want %d", offset, wantStaticLen)
	}

	dynamic := packed[wantStaticLen:]
	length := new(big.Int).SetBytes(dynamic[0:32]).Uint64()
	if length != uint64(len(nestedPayload)) {
		t.Fatalf("dynamic length prefix = %d, want %d", length, len(nestedPayload))
	}
	if !bytes.Equal(dynamic[32:], nestedPayload) {
		t.Fatalf("dynamic payload = %x, want %x", dynamic[32:], nestedPayload)
	}
}

func TestOwnerManagementCalldata_EncodesExpectedSelectors(t *testing.T) {
	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	prevOwner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	threshold := big.NewInt(2)

	addData, err := EncodeAddOwnerWithThresholdCalldata(owner, threshold)
	if err != nil {
		t.Fatalf("EncodeAddOwnerWithThresholdCalldata: %v", err)
	}
	if _, err := safeABI.Methods["addOwnerWithThreshold"].Inputs.Unpack(addData[4:]); err != nil {
		t.Fatalf("addOwnerWithThreshold calldata did not decode against its own ABI: %v", err)
	}

	removeData, err := EncodeRemoveOwnerCalldata(prevOwner, owner, threshold)
	if err != nil {
		t.Fatalf("EncodeRemoveOwnerCalldata: %v", err)
	}
	if _, err := safeABI.Methods["removeOwner"].Inputs.Unpack(removeData[4:]); err != nil {
		t.Fatalf("removeOwner calldata did not decode against its own ABI: %v", err)
	}

	changeData, err := EncodeChangeThresholdCalldata(threshold)
	if err != nil {
		t.Fatalf("EncodeChangeThresholdCalldata: %v", err)
	}
	if _, err := safeABI.Methods["changeThreshold"].Inputs.Unpack(changeData[4:]); err != nil {
		t.Fatalf("changeThreshold calldata did not decode against its own ABI: %v", err)
	}

	getOwnersData, err := EncodeGetOwnersCalldata()
	if err != nil {
		t.Fatalf("EncodeGetOwnersCalldata: %v", err)
	}
	if len(getOwnersData) != 4 {
		t.Fatalf("getOwners calldata should be just its 4-byte selector, got %d bytes", len(getOwnersData))
	}

	packedOwners, err := safeABI.Methods["getOwners"].Outputs.Pack([]common.Address{owner, prevOwner})
	if err != nil {
		t.Fatalf("pack fixture getOwners result: %v", err)
	}
	decoded, err := DecodeGetOwnersResult(packedOwners)
	if err != nil {
		t.Fatalf("DecodeGetOwnersResult: %v", err)
	}
	if len(decoded) != 2 || decoded[0] != owner || decoded[1] != prevOwner {
		t.Fatalf("DecodeGetOwnersResult = %v, want [%s %s]", decoded, owner.Hex(), prevOwner.Hex())
	}
}

func TestSwapOwnerAndSetGuardCalldata_EncodeExpectedSelectors(t *testing.T) {
	prevOwner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	oldOwner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	newOwner := common.HexToAddress("0x3333333333333333333333333333333333333333")

	swapData, err := EncodeSwapOwnerCalldata(prevOwner, oldOwner, newOwner)
	if err != nil {
		t.Fatalf("EncodeSwapOwnerCalldata: %v", err)
	}
	unpacked, err := safeABI.Methods["swapOwner"].Inputs.Unpack(swapData[4:])
	if err != nil {
		t.Fatalf("swapOwner calldata did not decode against its own ABI: %v", err)
	}
	if unpacked[0].(common.Address) != prevOwner || unpacked[1].(common.Address) != oldOwner || unpacked[2].(common.Address) != newOwner {
		t.Fatalf("swapOwner args = %v, want [%s %s %s]", unpacked, prevOwner.Hex(), oldOwner.Hex(), newOwner.Hex())
	}

	guard := common.HexToAddress("0x4444444444444444444444444444444444444444")
	guardData, err := EncodeSetGuardCalldata(guard)
	if err != nil {
		t.Fatalf("EncodeSetGuardCalldata: %v", err)
	}
	guardUnpacked, err := safeABI.Methods["setGuard"].Inputs.Unpack(guardData[4:])
	if err != nil {
		t.Fatalf("setGuard calldata did not decode against its own ABI: %v", err)
	}
	if guardUnpacked[0].(common.Address) != guard {
		t.Fatalf("setGuard arg = %v, want %s", guardUnpacked[0], guard.Hex())
	}
}

func TestFindPrevOwner(t *testing.T) {
	a := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	b := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	c := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
	owners := []common.Address{a, b, c}

	if prev, err := FindPrevOwner(owners, a); err != nil || prev != SentinelOwner {
		t.Fatalf("FindPrevOwner(a) = %s, %v; want SentinelOwner, nil", prev.Hex(), err)
	}
	if prev, err := FindPrevOwner(owners, b); err != nil || prev != a {
		t.Fatalf("FindPrevOwner(b) = %s, %v; want %s, nil", prev.Hex(), err, a.Hex())
	}
	if prev, err := FindPrevOwner(owners, c); err != nil || prev != b {
		t.Fatalf("FindPrevOwner(c) = %s, %v; want %s, nil", prev.Hex(), err, b.Hex())
	}
	other := common.HexToAddress("0xdddddddddddddddddddddddddddddddddddddddd")
	if _, err := FindPrevOwner(owners, other); err == nil {
		t.Fatal("expected an error for an address that isn't a current owner")
	}
}

func TestEncodeMessageDataForSafe_RoundTripsThroughHash(t *testing.T) {
	message := []byte("arbitrary pre-image bytes, e.g. an outer SafeTx's EncodeTransactionData")
	data := EncodeMessageDataForSafe(fixedDomainSeparator, message)
	want := crypto.Keccak256Hash(data)
	got := MessageHashForSafe(fixedDomainSeparator, message)
	if got != want {
		t.Fatalf("MessageHashForSafe = %s, want keccak256(EncodeMessageDataForSafe(...)) = %s", got.Hex(), want.Hex())
	}
	if data[0] != 0x19 || data[1] != 0x01 {
		t.Fatalf("EncodeMessageDataForSafe did not start with the 0x19 0x01 EIP-712 prefix")
	}
}
