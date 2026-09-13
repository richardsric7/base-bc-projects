//! The encrypted vault format: Argon2id-derived key + AES-256-GCM,
//! replacing the original app's PBKDF2-at-10,000-iterations-with-a-
//! public-data salt scheme (PLAN.md §1.2 findings 1-2, §5).
//!
//! A `VaultRecord` is exactly what IndexedDB stores (PLAN.md §5.1) - only
//! ciphertext and the public KDF parameters needed to reproduce it ever
//! touch disk. Nothing in this module ever returns a mnemonic except
//! `decrypt`, called only from inside the worker for the brief moment a
//! signing operation needs it (PLAN.md §4.1).

use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Key, Nonce};
use argon2::{Algorithm, Argon2, Params, Version};
use base64::engine::general_purpose::STANDARD as base64_engine;
use base64::Engine;
use serde::{Deserialize, Serialize};
use zeroize::Zeroize;

const SALT_LEN: usize = 16;
const NONCE_LEN: usize = 12;
const KEY_LEN: usize = 32;

/// OWASP's Argon2id baseline floor (PLAN.md §5.2) - ~19 MiB memory, 2
/// iterations, 1 degree of parallelism. Tunable upward at implementation/
/// deployment time once real target-device unlock latency is measured;
/// stored per-record so an already-encrypted vault keeps working even if
/// a later app version raises the default for new vaults.
const DEFAULT_MEMORY_KIB: u32 = 19_456;
const DEFAULT_ITERATIONS: u32 = 2;
const DEFAULT_PARALLELISM: u32 = 1;

#[derive(Debug)]
pub enum VaultError {
    Encryption,
    WrongPasswordOrCorruptVault,
    InvalidRecord,
}

impl core::fmt::Display for VaultError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            VaultError::Encryption => write!(f, "failed to encrypt vault"),
            VaultError::WrongPasswordOrCorruptVault => {
                write!(f, "incorrect password or corrupt vault data")
            }
            VaultError::InvalidRecord => write!(f, "invalid vault record"),
        }
    }
}

#[derive(Serialize, Deserialize, Clone)]
pub struct KdfParams {
    #[serde(rename = "memoryKiB")]
    pub memory_kib: u32,
    pub iterations: u32,
    pub parallelism: u32,
}

impl Default for KdfParams {
    fn default() -> Self {
        KdfParams {
            memory_kib: DEFAULT_MEMORY_KIB,
            iterations: DEFAULT_ITERATIONS,
            parallelism: DEFAULT_PARALLELISM,
        }
    }
}

/// Exactly PLAN.md §5.1's `VaultRecord` shape - the JSON this crate hands
/// back to JS to store in IndexedDB, and the JSON JS hands back for
/// `decrypt`. `role`/`address` are not secret and travel alongside the
/// ciphertext so the UI can label a locked wallet without unlocking it.
#[derive(Serialize, Deserialize, Clone)]
pub struct VaultRecord {
    pub role: String,
    pub address: String,
    pub kdf: String,
    #[serde(rename = "kdfParams")]
    pub kdf_params: KdfParams,
    pub salt: String,
    pub nonce: String,
    pub ciphertext: String,
    #[serde(rename = "createdAt")]
    pub created_at: u64,
}

/// Encrypts `plaintext` (a mnemonic phrase) under a key derived from
/// `password` via Argon2id with a fresh random salt, then AES-256-GCM
/// with a fresh random nonce. `created_at` is supplied by the caller
/// (JS's `Date.now()`) rather than read from a clock in this crate, since
/// timestamping isn't a cryptographic concern and keeping it out lets
/// this module stay clock-free and portable to native test builds.
pub fn encrypt(
    role: &str,
    address: &str,
    plaintext: &str,
    password: &str,
    created_at: u64,
) -> Result<VaultRecord, VaultError> {
    let mut salt = [0u8; SALT_LEN];
    getrandom::fill(&mut salt).map_err(|_| VaultError::Encryption)?;

    let kdf_params = KdfParams::default();
    let mut key_bytes = derive_key(password, &salt, &kdf_params)?;

    let mut nonce_bytes = [0u8; NONCE_LEN];
    getrandom::fill(&mut nonce_bytes).map_err(|_| VaultError::Encryption)?;

    let cipher = Aes256Gcm::new(&Key::<Aes256Gcm>::from(key_bytes));
    let nonce = Nonce::from(nonce_bytes);
    let ciphertext = cipher
        .encrypt(&nonce, plaintext.as_bytes())
        .map_err(|_| VaultError::Encryption)?;

    key_bytes.zeroize();

    Ok(VaultRecord {
        role: role.to_string(),
        address: address.to_string(),
        kdf: "argon2id".to_string(),
        kdf_params,
        salt: base64_engine.encode(salt),
        nonce: base64_engine.encode(nonce_bytes),
        ciphertext: base64_engine.encode(ciphertext),
        created_at,
    })
}

/// Decrypts a `VaultRecord` with `password`, returning the original
/// plaintext mnemonic. A wrong password and a corrupted/tampered
/// ciphertext are indistinguishable to the caller - AES-GCM's
/// authentication tag simply fails to verify in both cases, and this
/// function reports both as `WrongPasswordOrCorruptVault` rather than
/// leaking which one happened.
pub fn decrypt(record: &VaultRecord, password: &str) -> Result<String, VaultError> {
    let salt = base64_engine
        .decode(&record.salt)
        .map_err(|_| VaultError::InvalidRecord)?;
    let nonce_bytes = base64_engine
        .decode(&record.nonce)
        .map_err(|_| VaultError::InvalidRecord)?;
    let ciphertext = base64_engine
        .decode(&record.ciphertext)
        .map_err(|_| VaultError::InvalidRecord)?;

    let nonce_array: [u8; NONCE_LEN] = nonce_bytes
        .try_into()
        .map_err(|_| VaultError::InvalidRecord)?;

    let mut key_bytes = derive_key(password, &salt, &record.kdf_params)?;
    let cipher = Aes256Gcm::new(&Key::<Aes256Gcm>::from(key_bytes));
    let nonce = Nonce::from(nonce_array);

    let plaintext = cipher
        .decrypt(&nonce, ciphertext.as_ref())
        .map_err(|_| VaultError::WrongPasswordOrCorruptVault)?;

    key_bytes.zeroize();

    String::from_utf8(plaintext).map_err(|_| VaultError::WrongPasswordOrCorruptVault)
}

fn derive_key(password: &str, salt: &[u8], params: &KdfParams) -> Result<[u8; KEY_LEN], VaultError> {
    let argon2_params = Params::new(
        params.memory_kib,
        params.iterations,
        params.parallelism,
        Some(KEY_LEN),
    )
    .map_err(|_| VaultError::Encryption)?;
    let argon2 = Argon2::new(Algorithm::Argon2id, Version::V0x13, argon2_params);

    let mut key = [0u8; KEY_LEN];
    argon2
        .hash_password_into(password.as_bytes(), salt, &mut key)
        .map_err(|_| VaultError::Encryption)?;
    Ok(key)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn encrypts_and_decrypts_round_trip() {
        let record = encrypt("primary", "0xabc", "test mnemonic phrase", "correct horse", 1_700_000_000_000).unwrap();
        let plaintext = decrypt(&record, "correct horse").unwrap();
        assert_eq!(plaintext, "test mnemonic phrase");
    }

    #[test]
    fn wrong_password_fails_to_decrypt() {
        let record = encrypt("signer", "0xdef", "another mnemonic", "right password", 0).unwrap();
        let result = decrypt(&record, "wrong password");
        assert!(result.is_err());
    }

    #[test]
    fn salt_and_nonce_are_random_per_encryption() {
        let a = encrypt("primary", "0xabc", "same phrase", "same password", 0).unwrap();
        let b = encrypt("primary", "0xabc", "same phrase", "same password", 0).unwrap();
        // Fixes PLAN.md §1.2 finding 2: a salt derived only from public
        // data (like the address, which is identical here) would be
        // identical across these two calls. A random salt never is.
        assert_ne!(a.salt, b.salt);
        assert_ne!(a.nonce, b.nonce);
        assert_ne!(a.ciphertext, b.ciphertext);
    }

    #[test]
    fn tampered_ciphertext_is_rejected() {
        let mut record = encrypt("primary", "0xabc", "test mnemonic", "password123", 0).unwrap();
        let mut bytes = base64_engine.decode(&record.ciphertext).unwrap();
        bytes[0] ^= 0xff;
        record.ciphertext = base64_engine.encode(bytes);
        assert!(decrypt(&record, "password123").is_err());
    }
}
