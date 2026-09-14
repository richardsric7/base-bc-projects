package safe

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// domainSeparatorTypehash = keccak256("EIP712Domain(uint256 chainId,address verifyingContract)"),
// safeTxTypehash = keccak256("SafeTx(address to,uint256 value,bytes data,uint8 operation,uint256 safeTxGas,uint256 baseGas,uint256 gasPrice,address gasToken,address refundReceiver,uint256 nonce)"),
// and safeMessageTypehash = keccak256("SafeMessage(bytes message)") - the
// three EIP-712 typehash constants Safe.sol and
// CompatibilityFallbackHandler.sol hardcode. safe_test.go recomputes each
// from its signature string and asserts it matches the literal hex below,
// which is itself copied verbatim from the audited Solidity source - so a
// mistake in either copy shows up as a failing test rather than a
// silently wrong wallet address.
var (
	domainSeparatorTypehash = common.HexToHash("0x47e79534a245952e8b16893a336b85a3d9ea9fa8c573f3d803afb92a79469218")
	safeTxTypehash          = common.HexToHash("0xbb8310d486368db6bd6f849402fdd73ad53d316b5a4b2644ad6efe0f941286d8")
	safeMessageTypehash     = common.HexToHash("0x60b3cbf8b4a223d68d641b3b6ddf9a298e7f33710cf3d3a9d1146b5a6150fbca")
)

// SafeTx holds the ten fields Safe.execTransaction hashes and signs over.
// This codebase always leaves SafeTxGas, BaseGas, GasPrice, GasToken and
// RefundReceiver at their zero values (see EncodeExecTransactionCalldata's
// doc comment for why), but DomainSeparator, SafeTxHash and
// EncodeTransactionData all take the full struct so the hash this package
// computes can never silently diverge from the calldata
// EncodeExecTransactionCalldata builds for the same logical transaction.
type SafeTx struct {
	To             common.Address
	Value          *big.Int
	Data           []byte
	Operation      Operation
	SafeTxGas      *big.Int
	BaseGas        *big.Int
	GasPrice       *big.Int
	GasToken       common.Address
	RefundReceiver common.Address
	Nonce          *big.Int
}

// DomainSeparator computes Safe.domainSeparator() for a Safe deployed at
// safeAddress on the chain identified by chainID:
// keccak256(abi.encode(DOMAIN_SEPARATOR_TYPEHASH, chainId, this)).
func DomainSeparator(chainID *big.Int, safeAddress common.Address) common.Hash {
	return crypto.Keccak256Hash(
		domainSeparatorTypehash.Bytes(),
		common.LeftPadBytes(chainID.Bytes(), 32),
		common.LeftPadBytes(safeAddress.Bytes(), 32),
	)
}

// hashSafeTx computes keccak256(abi.encode(SAFE_TX_TYPEHASH, ...tx fields...)),
// the inner hash encodeTransactionData wraps in the EIP-712 envelope.
func hashSafeTx(tx SafeTx) common.Hash {
	return crypto.Keccak256Hash(
		safeTxTypehash.Bytes(),
		common.LeftPadBytes(tx.To.Bytes(), 32),
		common.LeftPadBytes(valueOrZero(tx.Value).Bytes(), 32),
		crypto.Keccak256(tx.Data),
		common.LeftPadBytes([]byte{byte(tx.Operation)}, 32),
		common.LeftPadBytes(valueOrZero(tx.SafeTxGas).Bytes(), 32),
		common.LeftPadBytes(valueOrZero(tx.BaseGas).Bytes(), 32),
		common.LeftPadBytes(valueOrZero(tx.GasPrice).Bytes(), 32),
		common.LeftPadBytes(tx.GasToken.Bytes(), 32),
		common.LeftPadBytes(tx.RefundReceiver.Bytes(), 32),
		common.LeftPadBytes(valueOrZero(tx.Nonce).Bytes(), 32),
	)
}

// EncodeTransactionData reproduces Safe.encodeTransactionData: the raw
// EIP-712 pre-image bytes (0x19 0x01 || domainSeparator || safeTxHash) a
// Safe owner ultimately signs over, directly or via the nested-message
// wrapping EncodeMessageDataForSafe performs for a contract-owner. Callers
// needing just the final hash should use SafeTxHash instead; this exists
// because EncodeMessageDataForSafe needs the pre-image bytes themselves,
// not just their hash, as the "message" a nested Safe wraps.
func EncodeTransactionData(domainSeparator common.Hash, tx SafeTx) []byte {
	safeTxHash := hashSafeTx(tx)
	data := make([]byte, 0, 2+32+32)
	data = append(data, 0x19, 0x01)
	data = append(data, domainSeparator.Bytes()...)
	data = append(data, safeTxHash.Bytes()...)
	return data
}

// SafeTxHash is the final digest Safe owners sign for tx - what
// Safe.getTransactionHash returns, and what checkNSignatures ultimately
// verifies signatures against (directly for an EOA owner, or wrapped per
// EncodeMessageDataForSafe when the signature comes from a contract
// owner).
func SafeTxHash(domainSeparator common.Hash, tx SafeTx) common.Hash {
	return crypto.Keccak256Hash(EncodeTransactionData(domainSeparator, tx))
}

// EncodeMessageDataForSafe reproduces
// CompatibilityFallbackHandler.encodeMessageDataForSafe: the EIP-712
// pre-image a Safe's OWN owners sign when that Safe is itself acting as a
// contract-owner (EIP-1271) of some other Safe. message is the raw bytes
// being "signed on behalf of" nestedSafe - for the nested-ownership case
// PLAN.md §13.4 relies on, that's the outer Safe's own
// EncodeTransactionData(...) pre-image, i.e. exactly what
// isValidSignature(bytes,bytes) receives as _data when the outer Safe's
// checkNSignatures calls into nestedSafe.
func EncodeMessageDataForSafe(nestedDomainSeparator common.Hash, message []byte) []byte {
	safeMessageHash := crypto.Keccak256Hash(safeMessageTypehash.Bytes(), crypto.Keccak256(message))
	data := make([]byte, 0, 2+32+32)
	data = append(data, 0x19, 0x01)
	data = append(data, nestedDomainSeparator.Bytes()...)
	data = append(data, safeMessageHash.Bytes()...)
	return data
}

// MessageHashForSafe is keccak256(EncodeMessageDataForSafe(...)) - the
// dataHash a nested Safe's own checkSignatures verifies its owners'
// signatures against, exactly as CompatibilityFallbackHandler.
// isValidSignature computes it before calling safe.checkSignatures.
func MessageHashForSafe(nestedDomainSeparator common.Hash, message []byte) common.Hash {
	return crypto.Keccak256Hash(EncodeMessageDataForSafe(nestedDomainSeparator, message))
}
