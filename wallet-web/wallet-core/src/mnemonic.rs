//! BIP-39 mnemonic generation/validation and BIP-32/44 HD derivation down
//! to the standard Ethereum account path `m/44'/60'/0'/0/0` - see
//! PLAN.md §4.4: this path is non-negotiable, since a mnemonic imported
//! from another EVM wallet must derive the *same* address here.
//!
//! HD derivation is hand-rolled against the BIP-32 spec directly (HMAC-
//! SHA512 + k256 scalar/point arithmetic) rather than pulling in a
//! third-party HD-wallet crate, keeping the dependency graph small and
//! auditable (PLAN.md §4.3).

use hmac::{Hmac, KeyInit, Mac};
use k256::elliptic_curve::sec1::ToSec1Point;
use k256::{PublicKey, Scalar, SecretKey};
use sha2::Sha512;
use sha3::{Digest, Keccak256};
use zeroize::{Zeroize, ZeroizeOnDrop};

use bip39::{Language, Mnemonic};

type HmacSha512 = Hmac<Sha512>;

/// The one derivation path this crate ever uses - the standard first
/// Ethereum account, matching MetaMask/Coinbase Wallet/every mainstream
/// EVM wallet (PLAN.md §4.4).
const DERIVATION_PATH: [u32; 5] = [
    44 | HARDENED,
    60 | HARDENED,
    0 | HARDENED,
    0,
    0,
];
const HARDENED: u32 = 0x8000_0000;

#[derive(Debug)]
pub enum WalletCoreError {
    InvalidMnemonic,
    InvalidWordCount,
    KeyDerivation,
}

impl core::fmt::Display for WalletCoreError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            WalletCoreError::InvalidMnemonic => write!(f, "invalid mnemonic phrase"),
            WalletCoreError::InvalidWordCount => write!(f, "word count must be 12 or 24"),
            WalletCoreError::KeyDerivation => write!(f, "key derivation failed"),
        }
    }
}

/// A derived secp256k1 keypair plus its checksummed EVM address. Zeroized
/// on drop so a dropped `DerivedKey` never leaves its scalar bytes behind
/// in the worker's linear memory (PLAN.md §4.1's zeroize guarantee).
#[derive(ZeroizeOnDrop)]
pub struct DerivedKey {
    #[zeroize(skip)]
    pub address: String,
    pub secret_bytes: [u8; 32],
}

/// Generates a fresh mnemonic of `word_count` words (12 or 24) using
/// entropy drawn from the browser's CSPRNG (via `getrandom`'s `wasm_js`
/// backend - see PLAN.md §4.3). Entropy is generated directly rather than
/// relying on bip39's own internal RNG choice, so exactly one, auditable
/// path ever produces randomness in this crate.
pub fn generate_mnemonic(word_count: u8) -> Result<String, WalletCoreError> {
    let entropy_bytes = match word_count {
        12 => 16,
        24 => 32,
        _ => return Err(WalletCoreError::InvalidWordCount),
    };
    let mut entropy = vec![0u8; entropy_bytes];
    getrandom::fill(&mut entropy).map_err(|_| WalletCoreError::KeyDerivation)?;
    let mnemonic = Mnemonic::from_entropy_in(Language::English, &entropy)
        .map_err(|_| WalletCoreError::InvalidMnemonic)?;
    entropy.zeroize();
    Ok(mnemonic.to_string())
}

/// Validates a mnemonic's wordlist membership and checksum.
pub fn validate_mnemonic(phrase: &str) -> bool {
    Mnemonic::parse_in_normalized(Language::English, phrase).is_ok()
}

/// Derives the standard Ethereum account key (`m/44'/60'/0'/0/0`) from a
/// mnemonic phrase and returns its checksummed address plus raw secret
/// key bytes, ready for `vault::encrypt` or `signing`.
pub fn derive(phrase: &str) -> Result<DerivedKey, WalletCoreError> {
    let mnemonic = Mnemonic::parse_in_normalized(Language::English, phrase)
        .map_err(|_| WalletCoreError::InvalidMnemonic)?;
    let mut seed = mnemonic.to_seed_normalized("");

    let (mut key, mut chain_code) = master_key_from_seed(&seed);
    seed.zeroize();

    for index in DERIVATION_PATH {
        let (child_key, child_chain_code) = derive_child(&key, &chain_code, index)?;
        key.zeroize();
        chain_code.zeroize();
        key = child_key;
        chain_code = child_chain_code;
    }
    chain_code.zeroize();

    let address = address_from_secret(&key)?;
    Ok(DerivedKey {
        address,
        secret_bytes: key,
    })
}

/// BIP-32 master key: `I = HMAC-SHA512(key = "Bitcoin seed", data = seed)`,
/// `IL` is the master secret key, `IR` the master chain code.
fn master_key_from_seed(seed: &[u8]) -> ([u8; 32], [u8; 32]) {
    let mut mac = HmacSha512::new_from_slice(b"Bitcoin seed").expect("HMAC accepts any key length");
    mac.update(seed);
    let i = mac.finalize().into_bytes();
    let mut il = [0u8; 32];
    let mut ir = [0u8; 32];
    il.copy_from_slice(&i[0..32]);
    ir.copy_from_slice(&i[32..64]);
    (il, ir)
}

/// One BIP-32 CKD-priv step. `index` with the high bit set (`HARDENED`)
/// derives a hardened child; otherwise a normal child.
fn derive_child(
    parent_key: &[u8; 32],
    parent_chain_code: &[u8; 32],
    index: u32,
) -> Result<([u8; 32], [u8; 32]), WalletCoreError> {
    let mut mac = HmacSha512::new_from_slice(parent_chain_code)
        .map_err(|_| WalletCoreError::KeyDerivation)?;

    if index & HARDENED != 0 {
        // Hardened: data = 0x00 || ser256(kpar) || ser32(i)
        mac.update(&[0u8]);
        mac.update(parent_key);
    } else {
        // Normal: data = serP(point(kpar)) || ser32(i)
        let parent_secret =
            SecretKey::from_slice(parent_key).map_err(|_| WalletCoreError::KeyDerivation)?;
        let parent_public = PublicKey::from_secret_scalar(&parent_secret.to_nonzero_scalar());
        mac.update(parent_public.to_sec1_point(true).as_bytes());
    }
    mac.update(&index.to_be_bytes());

    let i = mac.finalize().into_bytes();
    let il = &i[0..32];
    let ir = &i[32..64];

    let parent_scalar_secret =
        SecretKey::from_slice(parent_key).map_err(|_| WalletCoreError::KeyDerivation)?;
    let il_secret = SecretKey::from_slice(il).map_err(|_| WalletCoreError::KeyDerivation)?;

    let child_scalar: Scalar =
        *il_secret.to_nonzero_scalar().as_ref() + parent_scalar_secret.to_nonzero_scalar().as_ref();
    if bool::from(k256::elliptic_curve::group::ff::Field::is_zero(&child_scalar)) {
        // Probability ~0; BIP-32 says to derive the next index instead.
        // Treated as a derivation failure here since our fixed path never
        // realistically hits it.
        return Err(WalletCoreError::KeyDerivation);
    }

    let mut child_key = [0u8; 32];
    child_key.copy_from_slice(&child_scalar.to_bytes());
    let mut child_chain_code = [0u8; 32];
    child_chain_code.copy_from_slice(ir);

    Ok((child_key, child_chain_code))
}

/// Standard EVM address: last 20 bytes of Keccak-256(uncompressed pubkey
/// without its `0x04` prefix), EIP-55 checksum-cased.
fn address_from_secret(secret_bytes: &[u8; 32]) -> Result<String, WalletCoreError> {
    let secret = SecretKey::from_slice(secret_bytes).map_err(|_| WalletCoreError::KeyDerivation)?;
    let public = PublicKey::from_secret_scalar(&secret.to_nonzero_scalar());
    let uncompressed = public.to_sec1_point(false);
    let pubkey_bytes = uncompressed.as_bytes(); // 0x04 || X (32) || Y (32)

    let hash = Keccak256::digest(&pubkey_bytes[1..]);
    let address_bytes = &hash[12..32];
    Ok(to_checksum_address(address_bytes))
}

/// EIP-55 mixed-case checksum encoding.
fn to_checksum_address(address_bytes: &[u8]) -> String {
    let hex_lower: String = address_bytes.iter().map(|b| format!("{:02x}", b)).collect();
    let hash = Keccak256::digest(hex_lower.as_bytes());

    let mut checksummed = String::with_capacity(42);
    checksummed.push_str("0x");
    for (i, c) in hex_lower.chars().enumerate() {
        if c.is_ascii_digit() {
            checksummed.push(c);
            continue;
        }
        // nibble from the hash at this character's position selects case
        let byte = hash[i / 2];
        let nibble = if i % 2 == 0 { byte >> 4 } else { byte & 0x0f };
        if nibble >= 8 {
            checksummed.push(c.to_ascii_uppercase());
        } else {
            checksummed.push(c);
        }
    }
    checksummed
}

#[cfg(test)]
mod tests {
    use super::*;

    // BIP-39 standard test vector ("abandon abandon ... about") - widely
    // published expected address for m/44'/60'/0'/0/0 with an empty
    // passphrase, matching MetaMask's own well-known test mnemonic.
    const TEST_MNEMONIC: &str =
        "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about";
    const EXPECTED_ADDRESS: &str = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94";

    #[test]
    fn derives_known_test_vector_address() {
        let derived = derive(TEST_MNEMONIC).expect("derivation should succeed");
        assert_eq!(derived.address, EXPECTED_ADDRESS);
    }

    #[test]
    fn validates_and_rejects_mnemonics() {
        assert!(validate_mnemonic(TEST_MNEMONIC));
        assert!(!validate_mnemonic("not a real mnemonic phrase at all"));
    }

    #[test]
    fn generates_valid_mnemonics_of_requested_length() {
        let twelve = generate_mnemonic(12).unwrap();
        assert_eq!(twelve.split_whitespace().count(), 12);
        assert!(validate_mnemonic(&twelve));

        let twenty_four = generate_mnemonic(24).unwrap();
        assert_eq!(twenty_four.split_whitespace().count(), 24);
        assert!(validate_mnemonic(&twenty_four));
    }

    #[test]
    fn rejects_invalid_word_count() {
        assert!(generate_mnemonic(15).is_err());
    }

    #[test]
    fn checksum_matches_eip55_reference_vector() {
        // EIP-55 spec's own published test vector.
        let addr = "5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed";
        let bytes: Vec<u8> = (0..addr.len())
            .step_by(2)
            .map(|i| u8::from_str_radix(&addr[i..i + 2], 16).unwrap())
            .collect();
        assert_eq!(
            to_checksum_address(&bytes),
            "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
        );
    }
}
