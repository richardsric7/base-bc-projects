//! EIP-191 `personal_sign` (for SIWE, PLAN.md §2) and EIP-1559 (type
//! `0x02`) transaction signing matching the exact JSON shape
//! `wallet-backend`'s `network.UnsignedTx` produces (PLAN.md §2, §6.3).
//! Every function here takes raw secret key bytes for the moment it's
//! called and never retains them - the caller (`lib.rs`) is responsible
//! for zeroizing its own copy immediately after.

use k256::ecdsa::signature::hazmat::PrehashSigner;
use k256::ecdsa::{RecoveryId, Signature, SigningKey};
use serde::Deserialize;
use sha3::{Digest, Keccak256};

use crate::rlp;

#[derive(Debug)]
pub enum SigningError {
    InvalidKey,
    InvalidTransactionJson,
    InvalidHexField(&'static str),
    UnsupportedTransactionType,
    SigningFailed,
}

impl core::fmt::Display for SigningError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            SigningError::InvalidKey => write!(f, "invalid private key"),
            SigningError::InvalidTransactionJson => write!(f, "invalid unsigned transaction JSON"),
            SigningError::InvalidHexField(field) => write!(f, "invalid hex value in field: {field}"),
            SigningError::UnsupportedTransactionType => {
                write!(f, "only EIP-1559 (type 0x2) transactions are supported")
            }
            SigningError::SigningFailed => write!(f, "signing operation failed"),
        }
    }
}

/// Mirrors `wallet-backend`'s `network.UnsignedTx` exactly (see
/// PLAN.md §2): `chainId`/`value`/`data`/`maxFeePerGas`/
/// `maxPriorityFeePerGas` are 0x-prefixed hex strings (so they round-trip
/// through JSON without precision loss for values wider than 64 bits);
/// `nonce`/`gas` are plain JSON numbers, matching their Go `uint64` type.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct UnsignedTx {
    chain_id: String,
    nonce: u64,
    #[serde(default)]
    to: Option<String>,
    value: String,
    data: String,
    gas: u64,
    max_fee_per_gas: String,
    max_priority_fee_per_gas: String,
    #[serde(rename = "type")]
    tx_type: String,
}

/// Signs `message` per EIP-191's `personal_sign` convention:
/// `keccak256("\x19Ethereum Signed Message:\n" + len(message) + message)`,
/// ECDSA-signed and returned as a 65-byte `0x`-prefixed hex string
/// (`r || s || v`, `v = recoveryId + 27` - the legacy convention every
/// EVM signature-verification library, including `siwe-go`'s `Verify`
/// server-side, expects).
pub fn sign_personal_message(secret_bytes: &[u8; 32], message: &str) -> Result<String, SigningError> {
    let hash = personal_message_hash(message);
    let (signature, recovery_id) = sign_prehash(secret_bytes, &hash)?;

    let mut out = Vec::with_capacity(65);
    out.extend_from_slice(&signature.r().to_bytes());
    out.extend_from_slice(&signature.s().to_bytes());
    out.push(recovery_id.to_byte() + 27);
    Ok(format!("0x{}", hex::encode(out)))
}

fn personal_message_hash(message: &str) -> [u8; 32] {
    let prefix = format!("\x19Ethereum Signed Message:\n{}", message.len());
    let mut hasher = Keccak256::new();
    hasher.update(prefix.as_bytes());
    hasher.update(message.as_bytes());
    hasher.finalize().into()
}

/// Parses, RLP-encodes, and signs an EIP-1559 unsigned transaction JSON
/// (`wallet-backend`'s `POST /payments/build`/`/swaps/approve/build`
/// response body), returning the final signed raw transaction as a
/// `0x`-prefixed hex string ready for `POST /payments/submit` (PLAN.md
/// §6.3). Uses `yParity` (0 or 1), not legacy `v` - the encoding EIP-1559
/// transactions require.
pub fn sign_eip1559_transaction(secret_bytes: &[u8; 32], unsigned_tx_json: &str) -> Result<String, SigningError> {
    let tx: UnsignedTx =
        serde_json::from_str(unsigned_tx_json).map_err(|_| SigningError::InvalidTransactionJson)?;
    if tx.tx_type != "0x2" {
        return Err(SigningError::UnsupportedTransactionType);
    }

    let chain_id = hex_to_uint_bytes(&tx.chain_id, "chainId")?;
    let max_priority_fee = hex_to_uint_bytes(&tx.max_priority_fee_per_gas, "maxPriorityFeePerGas")?;
    let max_fee = hex_to_uint_bytes(&tx.max_fee_per_gas, "maxFeePerGas")?;
    let value = hex_to_uint_bytes(&tx.value, "value")?;
    let to_bytes = match &tx.to {
        Some(addr) if !addr.is_empty() => hex_to_bytes(addr, "to")?,
        _ => Vec::new(), // contract creation - unused by wallet-backend's template, kept for completeness
    };
    let data_bytes = hex_to_bytes(&tx.data, "data")?;

    let payload_fields = vec![
        rlp::encode_uint_bytes(&chain_id),
        rlp::encode_uint(tx.nonce),
        rlp::encode_uint_bytes(&max_priority_fee),
        rlp::encode_uint_bytes(&max_fee),
        rlp::encode_uint(tx.gas),
        rlp::encode_bytes(&to_bytes),
        rlp::encode_uint_bytes(&value),
        rlp::encode_bytes(&data_bytes),
        rlp::encode_list(&[]), // access list - empty, unused by wallet-backend's template
    ];
    let unsigned_payload = rlp::encode_list(&payload_fields);

    let mut hash_input = vec![0x02u8];
    hash_input.extend_from_slice(&unsigned_payload);
    let hash: [u8; 32] = Keccak256::digest(&hash_input).into();

    let (signature, recovery_id) = sign_prehash(secret_bytes, &hash)?;

    let signed_fields = vec![
        rlp::encode_uint_bytes(&chain_id),
        rlp::encode_uint(tx.nonce),
        rlp::encode_uint_bytes(&max_priority_fee),
        rlp::encode_uint_bytes(&max_fee),
        rlp::encode_uint(tx.gas),
        rlp::encode_bytes(&to_bytes),
        rlp::encode_uint_bytes(&value),
        rlp::encode_bytes(&data_bytes),
        rlp::encode_list(&[]),
        rlp::encode_uint(recovery_id.to_byte() as u64), // yParity
        rlp::encode_uint_bytes(&signature.r().to_bytes()),
        rlp::encode_uint_bytes(&signature.s().to_bytes()),
    ];
    let signed_payload = rlp::encode_list(&signed_fields);

    let mut raw_tx = vec![0x02u8];
    raw_tx.extend_from_slice(&signed_payload);
    Ok(format!("0x{}", hex::encode(raw_tx)))
}

fn sign_prehash(secret_bytes: &[u8; 32], hash: &[u8; 32]) -> Result<(Signature, RecoveryId), SigningError> {
    let signing_key = SigningKey::from_bytes(secret_bytes.into()).map_err(|_| SigningError::InvalidKey)?;
    signing_key
        .sign_prehash(hash)
        .map_err(|_| SigningError::SigningFailed)
}

fn hex_to_bytes(value: &str, field: &'static str) -> Result<Vec<u8>, SigningError> {
    let stripped = value.strip_prefix("0x").unwrap_or(value);
    if stripped.is_empty() {
        return Ok(Vec::new());
    }
    let padded = if stripped.len() % 2 == 1 {
        format!("0{stripped}")
    } else {
        stripped.to_string()
    };
    hex::decode(padded).map_err(|_| SigningError::InvalidHexField(field))
}

fn hex_to_uint_bytes(value: &str, field: &'static str) -> Result<Vec<u8>, SigningError> {
    hex_to_bytes(value, field)
}

#[cfg(test)]
mod tests {
    use super::*;

    const TEST_SECRET: [u8; 32] = [
        0x4c, 0x0c, 0x9e, 0x77, 0xf3, 0x2f, 0x2e, 0x8e, 0x3f, 0x1f, 0x1f, 0x1c, 0x1a, 0x1b, 0x1d,
        0x1e, 0x2c, 0x2d, 0x2e, 0x2f, 0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39,
        0x3a, 0x3b,
    ];

    #[test]
    fn signs_personal_message_and_returns_65_byte_hex_signature() {
        let sig = sign_personal_message(&TEST_SECRET, "Sign in to wallet-web").unwrap();
        assert!(sig.starts_with("0x"));
        // 65 bytes = 130 hex chars + "0x"
        assert_eq!(sig.len(), 132);
        let v_byte = u8::from_str_radix(&sig[130..132], 16).unwrap();
        assert!(v_byte == 27 || v_byte == 28);
    }

    /// Stronger than a format check: recovers the actual signer's address
    /// from the signature (the same ECDSA-recovery `siwe-go`'s `Verify`
    /// performs server-side) and confirms it matches the key that signed
    /// - not just that the output looks like a signature.
    #[test]
    fn personal_sign_signature_recovers_to_signing_key_address() {
        let message = "example.com wants you to sign in with your Ethereum account";
        let sig_hex = sign_personal_message(&TEST_SECRET, message).unwrap();
        let sig_bytes = hex::decode(&sig_hex[2..]).unwrap();

        let hash = personal_message_hash(message);
        let signature = Signature::from_slice(&sig_bytes[0..64]).unwrap();
        let recovery_id = RecoveryId::from_byte(sig_bytes[64] - 27).unwrap();

        let recovered_key =
            k256::ecdsa::VerifyingKey::recover_from_prehash(&hash, &signature, recovery_id).unwrap();
        let expected_key = SigningKey::from_bytes((&TEST_SECRET).into())
            .unwrap()
            .verifying_key()
            .clone();
        assert_eq!(recovered_key, expected_key);
    }

    #[test]
    fn signs_eip1559_transaction_and_returns_type_2_raw_tx() {
        let unsigned = r#"{
            "chainId": "0x14a34",
            "nonce": 5,
            "to": "0x000000000000000000000000000000000000dead",
            "value": "0xde0b6b3a7640000",
            "data": "0x",
            "gas": 21000,
            "maxFeePerGas": "0x59682f00",
            "maxPriorityFeePerGas": "0x3b9aca00",
            "type": "0x2"
        }"#;
        let raw = sign_eip1559_transaction(&TEST_SECRET, unsigned).unwrap();
        assert!(raw.starts_with("0x02"));
        // Should decode as valid hex of reasonable transaction length.
        assert!(hex::decode(&raw[2..]).unwrap().len() > 60);
    }

    /// End-to-end correctness: RLP-decodes the signed transaction this
    /// crate produced, recomputes the same unsigned-payload hash
    /// independently, and confirms the embedded (yParity, r, s) actually
    /// recovers to the address that key controls - not just that the
    /// bytes look transaction-shaped. A real signing bug (wrong field
    /// order, wrong hash preimage, wrong yParity) would fail this even
    /// though the raw hex might still "look" plausible.
    #[test]
    fn eip1559_signature_recovers_to_signing_key_address() {
        let unsigned = r#"{
            "chainId": "0x14a34",
            "nonce": 5,
            "to": "0x000000000000000000000000000000000000dead",
            "value": "0xde0b6b3a7640000",
            "data": "0x",
            "gas": 21000,
            "maxFeePerGas": "0x59682f00",
            "maxPriorityFeePerGas": "0x3b9aca00",
            "type": "0x2"
        }"#;
        let raw = sign_eip1559_transaction(&TEST_SECRET, unsigned).unwrap();
        let raw_bytes = hex::decode(&raw[2..]).unwrap();
        assert_eq!(raw_bytes[0], 0x02);

        let items = rlp::decode_list_items(&raw_bytes[1..]).unwrap();
        assert_eq!(items.len(), 12);
        let y_parity = items[9].first().copied().unwrap_or(0);
        let r = &items[10];
        let s = &items[11];

        // Recompute the unsigned payload hash from the same 9 fields
        // (everything but the trailing signature triple). `items[0..9]`
        // are raw decoded payloads (their own RLP length prefixes already
        // stripped by decode_list_items), so each must be re-wrapped as
        // its own RLP item before re-assembling the list - encode_list
        // itself expects a slice of already-RLP-encoded items, it does
        // not encode raw payloads on your behalf.
        let mut unsigned_fields: Vec<Vec<u8>> = items[0..8].iter().map(|item| rlp::encode_bytes(item)).collect();
        unsigned_fields.push(rlp::encode_list(&[])); // accessList - always empty here
        let unsigned_payload = rlp::encode_list(&unsigned_fields);
        let mut hash_input = vec![0x02u8];
        hash_input.extend_from_slice(&unsigned_payload);
        let hash: [u8; 32] = Keccak256::digest(&hash_input).into();

        let mut r_padded = [0u8; 32];
        r_padded[32 - r.len()..].copy_from_slice(r);
        let mut s_padded = [0u8; 32];
        s_padded[32 - s.len()..].copy_from_slice(s);
        let mut sig_bytes = [0u8; 64];
        sig_bytes[0..32].copy_from_slice(&r_padded);
        sig_bytes[32..64].copy_from_slice(&s_padded);

        let signature = Signature::from_slice(&sig_bytes).unwrap();
        let recovery_id = RecoveryId::from_byte(y_parity).unwrap();
        let recovered_key =
            k256::ecdsa::VerifyingKey::recover_from_prehash(&hash, &signature, recovery_id).unwrap();
        let expected_key = SigningKey::from_bytes((&TEST_SECRET).into())
            .unwrap()
            .verifying_key()
            .clone();
        assert_eq!(recovered_key, expected_key);
    }

    #[test]
    fn rejects_non_eip1559_transaction_types() {
        let legacy = r#"{
            "chainId": "0x1", "nonce": 0, "value": "0x0", "data": "0x",
            "gas": 21000, "maxFeePerGas": "0x1", "maxPriorityFeePerGas": "0x1",
            "type": "0x0"
        }"#;
        assert!(sign_eip1559_transaction(&TEST_SECRET, legacy).is_err());
    }

    #[test]
    fn handles_odd_length_hex_values() {
        // "0x1" is a single hex digit - a realistic value the server
        // could send for a small number (e.g. nonce=1 hex-encoded).
        let unsigned = r#"{
            "chainId": "0x1", "nonce": 0, "value": "0x1", "data": "0x",
            "gas": 21000, "maxFeePerGas": "0x1", "maxPriorityFeePerGas": "0x1",
            "type": "0x2"
        }"#;
        assert!(sign_eip1559_transaction(&TEST_SECRET, unsigned).is_ok());
    }
}
