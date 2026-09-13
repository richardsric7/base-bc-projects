//! `wallet-core`: the sole place a mnemonic or private key ever exists in
//! cleartext (PLAN.md §4). Every `#[wasm_bindgen]` function below is the
//! *entire* surface the Worker host (`app/src/worker/walletCoreWorker.ts`)
//! calls - the main thread never imports this module directly (PLAN.md
//! §4.1/§4.2).
//!
//! Once `encrypt_vault` has produced a `VaultRecord` for a role, the only
//! way back to that role's key is `unlock`, and from that point on the
//! decrypted secret lives only in this module's own session map - keyed
//! by role, zeroized on `lock`, and never returned to JS. Only addresses
//! and signatures ever cross back out.
//!
//! Storage itself (writing/reading a `VaultRecord` to/from IndexedDB) is
//! deliberately **not** done here - it stays in the Worker's own
//! TypeScript, so this crate never needs a Web-API binding dependency for
//! a concern that has nothing to do with cryptography. This module only
//! ever produces or consumes plain JSON strings.

mod mnemonic;
mod rlp;
mod signing;
mod vault;

use std::cell::RefCell;
use std::collections::HashMap;

use serde::Serialize;
use wasm_bindgen::prelude::*;
use zeroize::Zeroizing;

pub use vault::VaultRecord;

thread_local! {
    /// Unlocked roles' secret key bytes, held only for the life of this
    /// Worker instance. `Zeroizing` guarantees the bytes are overwritten
    /// the moment an entry is removed or replaced (PLAN.md §4.1's
    /// zeroize-on-drop guarantee) - a `lock` call, a page/Worker
    /// teardown, or unlocking the same role again all scrub the previous
    /// value.
    static SESSION: RefCell<HashMap<String, Zeroizing<[u8; 32]>>> = RefCell::new(HashMap::new());
}

fn to_js_err<E: core::fmt::Display>(err: E) -> JsValue {
    JsValue::from_str(&err.to_string())
}

fn to_json<T: Serialize>(value: &T) -> Result<String, JsValue> {
    serde_json::to_string(value).map_err(to_js_err)
}

/// Generates a fresh mnemonic (12 or 24 words) via the browser's CSPRNG.
#[wasm_bindgen]
pub fn generate_mnemonic(word_count: u8) -> Result<String, JsValue> {
    mnemonic::generate_mnemonic(word_count).map_err(to_js_err)
}

/// Validates a mnemonic's wordlist membership and checksum.
#[wasm_bindgen]
pub fn validate_mnemonic(phrase: &str) -> bool {
    mnemonic::validate_mnemonic(phrase)
}

/// Derives and returns the address a mnemonic controls, without storing
/// anything - used to show "this mnemonic controls address 0x..." before
/// the user commits to importing it (PLAN.md §4.2, §7).
#[wasm_bindgen]
pub fn derive_preview_address(phrase: &str) -> Result<String, JsValue> {
    mnemonic::derive(phrase).map(|k| k.address.clone()).map_err(to_js_err)
}

/// Derives `phrase`'s address and encrypts `phrase` under `password`,
/// returning the `VaultRecord` JSON the Worker host should persist to
/// IndexedDB (PLAN.md §5.1). The address is derived here, not accepted
/// from the caller, so a `VaultRecord`'s `address` field can never drift
/// from what its own ciphertext actually decrypts to.
#[wasm_bindgen]
pub fn encrypt_vault(role: &str, phrase: &str, password: &str, created_at_ms: f64) -> Result<String, JsValue> {
    let derived = mnemonic::derive(phrase).map_err(to_js_err)?;
    let record = vault::encrypt(role, &derived.address, phrase, password, created_at_ms as u64)
        .map_err(to_js_err)?;
    to_json(&record)
}

#[derive(Serialize)]
struct UnlockResult {
    address: String,
}

/// Decrypts `vault_record_json` with `password` and holds the resulting
/// secret key in this Worker instance's memory only, keyed by `role`,
/// until `lock`/`lock_all` is called or the Worker is torn down. Returns
/// only the address - never the mnemonic or key.
#[wasm_bindgen]
pub fn unlock(role: &str, vault_record_json: &str, password: &str) -> Result<String, JsValue> {
    let record: VaultRecord = serde_json::from_str(vault_record_json).map_err(to_js_err)?;
    let phrase = vault::decrypt(&record, password).map_err(to_js_err)?;
    let derived = mnemonic::derive(&phrase).map_err(to_js_err)?;

    SESSION.with(|session| {
        session
            .borrow_mut()
            .insert(role.to_string(), Zeroizing::new(derived.secret_bytes));
    });

    to_json(&UnlockResult { address: derived.address.clone() })
}

/// True if `role` currently has a decrypted key held in memory.
#[wasm_bindgen]
pub fn is_unlocked(role: &str) -> bool {
    SESSION.with(|session| session.borrow().contains_key(role))
}

/// Zeroizes and drops `role`'s in-memory key, if any (PLAN.md §5.3's
/// explicit-lock and idle-lock paths both call this).
#[wasm_bindgen]
pub fn lock(role: &str) {
    SESSION.with(|session| {
        session.borrow_mut().remove(role);
    });
}

/// Locks every currently-unlocked role at once - used by idle-timeout and
/// cross-tab lock propagation (PLAN.md §5.3), where a single event should
/// clear both the signer and primary-wallet sessions together.
#[wasm_bindgen]
pub fn lock_all() {
    SESSION.with(|session| {
        session.borrow_mut().clear();
    });
}

fn require_unlocked(role: &str) -> Result<[u8; 32], JsValue> {
    SESSION.with(|session| {
        session
            .borrow()
            .get(role)
            .map(|bytes| **bytes)
            .ok_or_else(|| JsValue::from_str("wallet is locked for this role"))
    })
}

/// Signs a SIWE message (EIP-191 `personal_sign`) with `role`'s key - used
/// for the signer's login (PLAN.md §2, §3) and any future shared-access
/// approval signature.
#[wasm_bindgen]
pub fn sign_siwe_message(role: &str, message: &str) -> Result<String, JsValue> {
    let secret = require_unlocked(role)?;
    signing::sign_personal_message(&secret, message).map_err(to_js_err)
}

/// Signs an EIP-1559 unsigned transaction (matching `wallet-backend`'s
/// `network.UnsignedTx` JSON shape) with `role`'s key, returning the raw
/// signed transaction hex ready for `/payments/submit` or
/// `/swaps/approve/submit` (PLAN.md §6.3).
#[wasm_bindgen]
pub fn sign_transaction(role: &str, unsigned_tx_json: &str) -> Result<String, JsValue> {
    let secret = require_unlocked(role)?;
    signing::sign_eip1559_transaction(&secret, unsigned_tx_json).map_err(to_js_err)
}

/// Signs the PLAN.md §3 "link primary wallet" proof-of-control message
/// with the **primary wallet's** key specifically (never the signer's) -
/// a thin, semantically-named wrapper over `sign_siwe_message` so callers
/// can't accidentally pass the wrong role for this one security-critical
/// message.
#[wasm_bindgen]
pub fn sign_link_primary_message(message: &str) -> Result<String, JsValue> {
    sign_siwe_message("primary", message)
}
