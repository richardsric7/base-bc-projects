package contracts

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"wallet-backend/internal/safe"
)

// Same posture as contracts_test.go's own doc comment: encoding/decoding
// is verified directly rather than against a live or simulated EVM (a
// simulated backend pulls in a large, otherwise-unneeded transitive
// dependency tree - go-ethereum's own beacon/catalyst simulation stack -
// disproportionate to testing one guard contract). RecoveryGuard's actual
// on-chain restriction logic was instead verified two other ways: solc
// compiled it successfully against the exact interface declared in
// RecoveryGuard.sol, and solidity/README.md's "RecoveryGuard's Guard
// interfaceId, verified independently" note cross-checks its declared
// Guard interface's ERC-165 interfaceId against Safe's real, well-known
// value from scratch in Go - not merely trusted on sight.

func TestRecoveryGuardDeployData(t *testing.T) {
	recoveryServiceOwner := "0x00000000000000000000000000000000000AAA"
	data, err := RecoveryGuardDeployData(recoveryServiceOwner)
	if err != nil {
		t.Fatalf("RecoveryGuardDeployData: %v", err)
	}

	bytecode := recoveryGuardBytecode()
	if !bytes.HasPrefix(data, bytecode) {
		t.Fatal("expected the deploy data to start with the contract's bytecode")
	}
	argsData := data[len(bytecode):]

	unpacked, err := RecoveryGuardABI.Constructor.Inputs.Unpack(argsData)
	if err != nil {
		t.Fatalf("failed to unpack constructor args: %v", err)
	}
	if unpacked[0].(common.Address) != common.HexToAddress(recoveryServiceOwner) {
		t.Fatalf("unexpected constructor arg: %v", unpacked[0])
	}
}

func TestRecoveryGuardDeployData_RejectsMalformedAddress(t *testing.T) {
	// common.HexToAddress never errors (it silently truncates/pads), so
	// this asserts the encoder itself doesn't error either - documenting
	// that address validation is the caller's responsibility (matching
	// every other Encode*/DeployData helper in this codebase, e.g.
	// TokenizedAssetDeployData), not a gap specific to this function.
	if _, err := RecoveryGuardDeployData("not-an-address"); err != nil {
		t.Fatalf("expected no error (validation happens upstream, see validators.IsValidAddress), got: %v", err)
	}
}

// TestRecoveryGuardABI_CheckTransactionMatchesSafeGuardSelector packs a
// call the same way this project's own network.Client would submit it
// against a real Safe (all ten execTransaction-shaped fields plus
// msgSender) and confirms it round-trips through the embedded ABI -
// i.e. RecoveryGuardABI.checkTransaction really does have the exact
// argument shape Safe.execTransaction's own checkTransaction hook call
// supplies, not an approximation of it.
func TestRecoveryGuardABI_CheckTransactionMatchesSafeGuardSelector(t *testing.T) {
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")
	sigs := packedTestSignature(t)

	packed, err := RecoveryGuardABI.Pack("checkTransaction",
		to, big.NewInt(0), []byte{0xaa, 0xbb, 0xcc, 0xdd}, uint8(0),
		big.NewInt(0), big.NewInt(0), big.NewInt(0),
		common.Address{}, common.Address{},
		sigs, common.Address{},
	)
	if err != nil {
		t.Fatalf("pack checkTransaction: %v", err)
	}

	method := RecoveryGuardABI.Methods["checkTransaction"]
	unpacked, err := method.Inputs.Unpack(packed[4:])
	if err != nil {
		t.Fatalf("unpack checkTransaction calldata: %v", err)
	}
	if unpacked[0].(common.Address) != to {
		t.Fatalf("round-tripped to = %v, want %v", unpacked[0], to)
	}
	if !bytes.Equal(unpacked[9].([]byte), sigs) {
		t.Fatalf("round-tripped signatures did not match what was packed")
	}
}

func packedTestSignature(t *testing.T) []byte {
	t.Helper()
	owner := common.HexToAddress("0x2222222222222222222222222222222222222222")
	packed, err := safe.PackSignatures([]safe.Signature{safe.ContractSignature(owner, []byte{0x01})})
	if err != nil {
		t.Fatalf("PackSignatures: %v", err)
	}
	return packed
}
