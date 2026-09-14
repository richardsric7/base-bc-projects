// Package safe implements the on-chain primitives PLAN.md §13 needs to
// treat a Gnosis Safe (Safe{Wallet}) smart-contract account as the Base
// equivalent of a Stellar weighted-signer account: computing a Safe's
// counterfactual address before it is ever deployed, building the
// calldata that deploys and initializes one, and constructing the packed
// multi-signature blob execTransaction expects - including the EIP-1271
// "contract signature" encoding that lets one Safe be an owner of
// another. That nested-ownership mechanism is what PLAN.md §13.4 relies
// on: every sub-wallet or shared-access group references a participant's
// primary-wallet Safe address rather than a raw signer EOA, so rotating a
// user's signer key (wallet recovery) only ever touches their own primary
// Safe and never anything they've shared access to or from.
//
// Every address, bytecode blob, and typehash constant below is taken
// verbatim from Safe's official v1.4.1 canonical deployment
// (github.com/safe-global/safe-deployments for deployment addresses,
// github.com/safe-global/safe-contracts for the compiled creation code
// and Solidity source of the typehashes) - the same contracts already
// live at these exact addresses on Base mainnet (8453) and Base Sepolia
// (84532), deployed via Safe's chain-agnostic deterministic deployment
// proxy. Getting any of this wrong would compute a wallet address that
// funds could be sent to but never recovered from, so nothing here is
// derived or approximated by this codebase - safe_test.go cross-checks
// every typehash against a from-scratch keccak256 of its Solidity
// signature string, and the CREATE2 computation against the exact
// assembly in SafeProxyFactory.deployProxy.
package safe

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Canonical v1.4.1 contract addresses. Identical on every EVM chain Safe
// has deployed to via its deterministic deployer, Base mainnet and Base
// Sepolia included - there is deliberately no per-chain table here.
var (
	// ProxyFactoryAddress is SafeProxyFactory, the contract that CREATE2s
	// new SafeProxy instances via createProxyWithNonce.
	ProxyFactoryAddress = common.HexToAddress("0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67")

	// SingletonAddress is SafeL2, the logic contract every SafeProxy
	// delegatecalls into. SafeL2 (rather than plain Safe) is used
	// deliberately: it emits events for module-executed transactions,
	// which this codebase's own indexer (payment-history-engine) and
	// block-chasing worker need to see multisig activity at all on an L2
	// without a tracing node.
	SingletonAddress = common.HexToAddress("0x29fcB43b46531BcA003ddC8FCB67FFE91900C762")

	// CompatibilityFallbackHandlerAddress is the fallback handler every
	// Safe.setup call should install: it's what makes isValidSignature
	// (EIP-1271) work at all, including the nested-Safe-as-owner case
	// this package's signature packing supports.
	CompatibilityFallbackHandlerAddress = common.HexToAddress("0xfd0732Dc9E303f09fCEf3a7388Ad10A83459Ec99")
)

// proxyCreationCode is the exact, argument-free creation bytecode of
// SafeProxy.sol as compiled for the v1.4.1 release - i.e. exactly what
// type(SafeProxy).creationCode evaluates to on-chain, which is in turn
// exactly what SafeProxyFactory.proxyCreationCode() returns.
// createProxyWithNonce appends the 32-byte left-padded singleton address
// to this and CREATE2s the result; ComputeProxyAddress below reproduces
// that byte for byte.
var proxyCreationCode = common.FromHex("0x608060405234801561001057600080fd5b506040516101e63803806101e68339818101604052602081101561003357600080fd5b8101908080519060200190929190505050600073ffffffffffffffffffffffffffffffffffffffff168173ffffffffffffffffffffffffffffffffffffffff1614156100ca576040517f08c379a00000000000000000000000000000000000000000000000000000000081526004018080602001828103825260228152602001806101c46022913960400191505060405180910390fd5b806000806101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff1602179055505060ab806101196000396000f3fe608060405273ffffffffffffffffffffffffffffffffffffffff600054167fa619486e0000000000000000000000000000000000000000000000000000000060003514156050578060005260206000f35b3660008037600080366000845af43d6000803e60008114156070573d6000fd5b3d6000f3fea264697066735822122003d1488ee65e08fa41e58e888a9865554c535f2c77126a82cb4c0f917f31441364736f6c63430007060033496e76616c69642073696e676c65746f6e20616464726573732070726f7669646564")

// ComputeProxyAddress reproduces SafeProxyFactory.createProxyWithNonce's
// CREATE2 address computation exactly, so a wallet's address can be known
// - and safely funded or referenced by other wallets' owner lists - long
// before anyone spends gas actually deploying it (PLAN.md §13.11).
//
// initializer must be the exact calldata that will later be passed as
// createProxyWithNonce's own initializer argument (see
// EncodeSetupCalldata): the computed address changes if the initializer
// does, by design, since Safe folds keccak256(initializer) into the
// CREATE2 salt specifically so that two Safes with different owners never
// collide on the same address for the same saltNonce.
func ComputeProxyAddress(singleton common.Address, initializer []byte, saltNonce *big.Int) common.Address {
	if saltNonce == nil {
		saltNonce = big.NewInt(0)
	}
	salt := crypto.Keccak256(crypto.Keccak256(initializer), common.LeftPadBytes(saltNonce.Bytes(), 32))
	var salt32 [32]byte
	copy(salt32[:], salt)

	deploymentCode := make([]byte, 0, len(proxyCreationCode)+32)
	deploymentCode = append(deploymentCode, proxyCreationCode...)
	deploymentCode = append(deploymentCode, common.LeftPadBytes(singleton.Bytes(), 32)...)
	initCodeHash := crypto.Keccak256(deploymentCode)

	return crypto.CreateAddress2(ProxyFactoryAddress, salt32, initCodeHash)
}

// safeABI holds only the handful of Safe/SafeProxyFactory methods this
// codebase ever needs to encode a call to. Unlike the generic
// network.EncodeContractCall (built for arbitrary router calls whose ABI
// arrives at runtime from JSON), everything here is called with concrete
// Go types the caller already has, so a small purpose-built ABI is
// clearer than routing through JSON coercion.
const safeABIJSON = `[
	{"name":"setup","type":"function","inputs":[
		{"name":"_owners","type":"address[]"},
		{"name":"_threshold","type":"uint256"},
		{"name":"to","type":"address"},
		{"name":"data","type":"bytes"},
		{"name":"fallbackHandler","type":"address"},
		{"name":"paymentToken","type":"address"},
		{"name":"payment","type":"uint256"},
		{"name":"paymentReceiver","type":"address"}
	],"outputs":[]},
	{"name":"execTransaction","type":"function","inputs":[
		{"name":"to","type":"address"},
		{"name":"value","type":"uint256"},
		{"name":"data","type":"bytes"},
		{"name":"operation","type":"uint8"},
		{"name":"safeTxGas","type":"uint256"},
		{"name":"baseGas","type":"uint256"},
		{"name":"gasPrice","type":"uint256"},
		{"name":"gasToken","type":"address"},
		{"name":"refundReceiver","type":"address"},
		{"name":"signatures","type":"bytes"}
	],"outputs":[{"name":"success","type":"bool"}]},
	{"name":"createProxyWithNonce","type":"function","inputs":[
		{"name":"_singleton","type":"address"},
		{"name":"initializer","type":"bytes"},
		{"name":"saltNonce","type":"uint256"}
	],"outputs":[{"name":"proxy","type":"address"}]},
	{"name":"nonce","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
	{"name":"addOwnerWithThreshold","type":"function","inputs":[
		{"name":"owner","type":"address"},
		{"name":"_threshold","type":"uint256"}
	],"outputs":[]},
	{"name":"removeOwner","type":"function","inputs":[
		{"name":"prevOwner","type":"address"},
		{"name":"owner","type":"address"},
		{"name":"_threshold","type":"uint256"}
	],"outputs":[]},
	{"name":"changeThreshold","type":"function","inputs":[
		{"name":"_threshold","type":"uint256"}
	],"outputs":[]},
	{"name":"getOwners","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address[]"}]}
]`

var safeABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(safeABIJSON))
	if err != nil {
		panic("safe: invalid embedded Safe ABI: " + err.Error())
	}
	safeABI = parsed
}

// EncodeSetupCalldata ABI-encodes a call to Safe.setup with the given
// owners and threshold, CompatibilityFallbackHandlerAddress installed as
// the fallback handler, and no delegatecall or payment (to, data,
// paymentToken, payment and paymentReceiver all zeroed) - every Safe this
// codebase deploys is a plain multisig account, never one that runs
// arbitrary setup-time delegatecall logic or self-funds its own
// deployment gas. This is both what gets passed as createProxyWithNonce's
// initializer and, hashed, folded into the CREATE2 salt (see
// ComputeProxyAddress).
func EncodeSetupCalldata(owners []common.Address, threshold *big.Int) ([]byte, error) {
	return safeABI.Pack("setup",
		owners,
		threshold,
		common.Address{},
		[]byte{},
		CompatibilityFallbackHandlerAddress,
		common.Address{},
		big.NewInt(0),
		common.Address{},
	)
}

// EncodeCreateProxyWithNonceCalldata ABI-encodes the SafeProxyFactory call
// that actually deploys a Safe on-chain at the address ComputeProxyAddress
// predicted for the same (singleton, initializer, saltNonce) triple.
func EncodeCreateProxyWithNonceCalldata(singleton common.Address, initializer []byte, saltNonce *big.Int) ([]byte, error) {
	return safeABI.Pack("createProxyWithNonce", singleton, initializer, saltNonce)
}

// Operation mirrors Safe's Enum.Operation: 0 for a regular CALL, 1 for a
// DELEGATECALL. This codebase never has reason to ask a Safe to
// delegatecall (that would let the called contract rewrite the Safe's own
// storage) - OperationCall is exported so callers never have to spell out
// the magic number, and OperationDelegateCall exists only so a reviewer
// can see exactly what value is being deliberately avoided.
type Operation uint8

const (
	OperationCall         Operation = 0
	OperationDelegateCall Operation = 1
)

// EncodeExecTransactionCalldata ABI-encodes a call to Safe.execTransaction
// for tx, with the already-assembled packedSignatures (see PackSignatures)
// as the trailing signatures argument. gasPrice, baseGas and gasToken are
// always zero: this codebase's relayer pool (PLAN.md §13.12) pays
// execution gas directly as the transaction sender rather than asking the
// Safe to refund it in-band, so refundReceiver is meaningless here too and
// is always the zero address.
func EncodeExecTransactionCalldata(tx SafeTx, packedSignatures []byte) ([]byte, error) {
	return safeABI.Pack("execTransaction",
		tx.To,
		valueOrZero(tx.Value),
		tx.Data,
		uint8(tx.Operation),
		big.NewInt(0),    // safeTxGas
		big.NewInt(0),    // baseGas
		big.NewInt(0),    // gasPrice
		common.Address{}, // gasToken
		common.Address{}, // refundReceiver
		packedSignatures,
	)
}

func valueOrZero(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return v
}

// EncodeNonceCalldata ABI-encodes a call to Safe.nonce(), the view function
// a Safe's own current transaction nonce is read from - needed at proposal
// time (PLAN.md §13.10 Phase 4) to fix the nonce a SafeTx's hash, and every
// approver's signature over it, are computed against. Reading this value
// with a plain eth_call rather than reserving it atomically under a row
// lock is a known, deliberately deferred race (PLAN.md §13.12 risk 1,
// closed in Phase 6): two actions proposed concurrently against the same
// Safe can observe the same nonce and collide on-chain when the second
// tries to execute - acceptable for Phase 4, not for the long term.
func EncodeNonceCalldata() ([]byte, error) {
	return safeABI.Pack("nonce")
}

// DecodeNonceResult unpacks Safe.nonce()'s eth_call return data.
func DecodeNonceResult(data []byte) (*big.Int, error) {
	out, err := safeABI.Unpack("nonce", data)
	if err != nil {
		return nil, err
	}
	nonce, ok := out[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("safe: decode nonce: unexpected output type")
	}
	return nonce, nil
}

// EncodeAddOwnerWithThresholdCalldata ABI-encodes a call to
// Safe.addOwnerWithThreshold(owner, threshold) - a Safe's own owner-
// management self-call, proposed and approved through the exact same
// propose/approve/execute pipeline as any other action (PLAN.md §13.10
// Phase 5): its `to` is the Safe's own address, not some external
// contract.
func EncodeAddOwnerWithThresholdCalldata(owner common.Address, threshold *big.Int) ([]byte, error) {
	return safeABI.Pack("addOwnerWithThreshold", owner, threshold)
}

// EncodeRemoveOwnerCalldata ABI-encodes a call to
// Safe.removeOwner(prevOwner, owner, threshold). prevOwner is required by
// OwnerManager's singly-linked-list storage layout - see FindPrevOwner for
// how to compute it from Safe.getOwners()'s own return order.
func EncodeRemoveOwnerCalldata(prevOwner, owner common.Address, threshold *big.Int) ([]byte, error) {
	return safeABI.Pack("removeOwner", prevOwner, owner, threshold)
}

// EncodeChangeThresholdCalldata ABI-encodes a call to
// Safe.changeThreshold(threshold), with no other change to the owner set.
func EncodeChangeThresholdCalldata(threshold *big.Int) ([]byte, error) {
	return safeABI.Pack("changeThreshold", threshold)
}

// EncodeGetOwnersCalldata ABI-encodes a call to Safe.getOwners(), the view
// function returning a Safe's current owners in OwnerManager's own
// linked-list order (sentinel-first, not necessarily address-sorted) -
// needed to compute removeOwner's prevOwner argument.
func EncodeGetOwnersCalldata() ([]byte, error) {
	return safeABI.Pack("getOwners")
}

// DecodeGetOwnersResult unpacks Safe.getOwners()'s eth_call return data.
func DecodeGetOwnersResult(data []byte) ([]common.Address, error) {
	out, err := safeABI.Unpack("getOwners", data)
	if err != nil {
		return nil, err
	}
	owners, ok := out[0].([]common.Address)
	if !ok {
		return nil, fmt.Errorf("safe: decode getOwners: unexpected output type")
	}
	return owners, nil
}

// SentinelOwner is OwnerManager's SENTINEL_OWNERS constant (address(0x1)) -
// the linked list's head marker, and the correct prevOwner argument for
// removeOwner when the owner being removed is first in Safe.getOwners()'s
// own return order.
var SentinelOwner = common.HexToAddress("0x1")

// FindPrevOwner returns the owner immediately preceding target in owners
// (as returned by Safe.getOwners()) - or SentinelOwner if target is
// first - exactly the prevOwner argument Safe.removeOwner requires to
// unlink it. Returns an error if target isn't present in owners at all.
func FindPrevOwner(owners []common.Address, target common.Address) (common.Address, error) {
	prev := SentinelOwner
	for _, o := range owners {
		if o == target {
			return prev, nil
		}
		prev = o
	}
	return common.Address{}, fmt.Errorf("safe: %s is not a current owner", target.Hex())
}
