//! C ABI bindings for `dart:ffi`, used by `wallet-mobile` - the native
//! (Android/iOS/desktop) counterpart to `lib.rs`'s `wasm-bindgen` exports
//! for `wallet-web`. Every function here mirrors one `wasm-bindgen`
//! export exactly (same underlying `mnemonic`/`vault`/`signing` logic,
//! same session semantics - a decrypted key lives only in this module's
//! own in-memory map, keyed by role, until `ffi_lock`/`ffi_lock_all`),
//! just through a C-compatible ABI instead of `wasm-bindgen`'s JS glue:
//! null-terminated UTF-8 strings in, a heap-allocated null-terminated
//! UTF-8 string out (always freed by the caller via `ffi_free_string`),
//! and boolean-only functions returning a plain `u8` (0/1) since they
//! can't fail.
//!
//! Every fallible function returns a JSON envelope
//! `{"ok":true,"value":"..."}` or `{"ok":false,"error":"..."}` rather
//! than a bare value, so Dart always decodes one consistent shape
//! regardless of which function it called - see `wallet-mobile`'s
//! `lib/core/wallet_core_bindings.dart`.
//!
//! This is a distinct, independent session store from `lib.rs`'s -
//! harmless, since the two are compiled for disjoint targets in practice
//! (`wasm32` for `wallet-web`, the host triple for `wallet-mobile`) and
//! never run in the same process.

use std::cell::RefCell;
use std::collections::HashMap;
use std::ffi::{CStr, CString};
use std::os::raw::c_char;

use serde::Serialize;
use zeroize::Zeroizing;

use crate::mnemonic;
use crate::signing;
use crate::vault;

thread_local! {
    static FFI_SESSION: RefCell<HashMap<String, Zeroizing<[u8; 32]>>> = RefCell::new(HashMap::new());
}

#[derive(Serialize)]
struct Envelope<'a> {
    ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    value: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

fn ok_json(value: &str) -> *mut c_char {
    to_c_string(&Envelope { ok: true, value: Some(value), error: None })
}

fn err_json<E: core::fmt::Display>(err: E) -> *mut c_char {
    to_c_string(&Envelope { ok: false, value: None, error: Some(err.to_string()) })
}

fn to_c_string<T: Serialize>(value: &T) -> *mut c_char {
    // serde_json::to_string only fails on non-UTF8 map keys or a type
    // whose Serialize impl itself errors - never the case for the plain
    // Envelope above, so the fallback here is unreachable in practice
    // but still returns a valid, freeable C string rather than a null
    // pointer Dart would have to special-case.
    let json = serde_json::to_string(value).unwrap_or_else(|_| "{\"ok\":false,\"error\":\"internal encoding error\"}".to_string());
    CString::new(json).unwrap_or_else(|_| CString::new("{\"ok\":false,\"error\":\"internal encoding error\"}").unwrap()).into_raw()
}

/// # Safety
/// `s` must be a valid, null-terminated UTF-8 C string pointer, or null
/// (treated as an empty string) - never a dangling or otherwise invalid
/// pointer.
unsafe fn from_c_str(s: *const c_char) -> String {
    if s.is_null() {
        return String::new();
    }
    CStr::from_ptr(s).to_string_lossy().into_owned()
}

/// Frees a string this module returned. Every `ffi_*` function below
/// that returns `*mut c_char` must have its result passed here exactly
/// once - never freed with anything but this (the allocator on the Dart
/// side is not the allocator that produced it).
///
/// # Safety
/// `s` must be a pointer this module itself returned, and not already
/// freed.
#[no_mangle]
pub unsafe extern "C" fn ffi_free_string(s: *mut c_char) {
    if !s.is_null() {
        drop(CString::from_raw(s));
    }
}

/// # Safety
/// `word_count` should be 12 or 24 (`mnemonic::generate_mnemonic`
/// itself validates and reports any other value as an error in the
/// envelope, so an invalid value can't corrupt memory - just returns an
/// `{"ok":false,...}` result).
#[no_mangle]
pub extern "C" fn ffi_generate_mnemonic(word_count: u8) -> *mut c_char {
    match mnemonic::generate_mnemonic(word_count) {
        Ok(phrase) => ok_json(&phrase),
        Err(e) => err_json(e),
    }
}

/// # Safety
/// `phrase` must be a valid null-terminated UTF-8 C string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_validate_mnemonic(phrase: *const c_char) -> u8 {
    mnemonic::validate_mnemonic(&from_c_str(phrase)) as u8
}

/// # Safety
/// `phrase` must be a valid null-terminated UTF-8 C string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_derive_preview_address(phrase: *const c_char) -> *mut c_char {
    match mnemonic::derive(&from_c_str(phrase)) {
        Ok(derived) => ok_json(&derived.address),
        Err(e) => err_json(e),
    }
}

/// # Safety
/// `role`/`phrase`/`password` must each be a valid null-terminated UTF-8
/// C string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_encrypt_vault(
    role: *const c_char,
    phrase: *const c_char,
    password: *const c_char,
    created_at_ms: f64,
) -> *mut c_char {
    let role = from_c_str(role);
    let phrase = from_c_str(phrase);
    let password = from_c_str(password);

    let derived = match mnemonic::derive(&phrase) {
        Ok(d) => d,
        Err(e) => return err_json(e),
    };
    match vault::encrypt(&role, &derived.address, &phrase, &password, created_at_ms as u64) {
        Ok(record) => match serde_json::to_string(&record) {
            Ok(json) => ok_json(&json),
            Err(e) => err_json(e),
        },
        Err(e) => err_json(e),
    }
}

/// # Safety
/// `role`/`vault_record_json`/`password` must each be a valid
/// null-terminated UTF-8 C string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_unlock(
    role: *const c_char,
    vault_record_json: *const c_char,
    password: *const c_char,
) -> *mut c_char {
    let role = from_c_str(role);
    let vault_record_json = from_c_str(vault_record_json);
    let password = from_c_str(password);

    let record: vault::VaultRecord = match serde_json::from_str(&vault_record_json) {
        Ok(r) => r,
        Err(e) => return err_json(e),
    };
    let phrase = match vault::decrypt(&record, &password) {
        Ok(p) => p,
        Err(e) => return err_json(e),
    };
    let derived = match mnemonic::derive(&phrase) {
        Ok(d) => d,
        Err(e) => return err_json(e),
    };

    FFI_SESSION.with(|session| {
        session.borrow_mut().insert(role, Zeroizing::new(derived.secret_bytes));
    });

    ok_json(&derived.address)
}

/// # Safety
/// `role` must be a valid null-terminated UTF-8 C string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_is_unlocked(role: *const c_char) -> u8 {
    let role = from_c_str(role);
    FFI_SESSION.with(|session| session.borrow().contains_key(&role)) as u8
}

/// # Safety
/// `role` must be a valid null-terminated UTF-8 C string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_lock(role: *const c_char) {
    let role = from_c_str(role);
    FFI_SESSION.with(|session| {
        session.borrow_mut().remove(&role);
    });
}

#[no_mangle]
pub extern "C" fn ffi_lock_all() {
    FFI_SESSION.with(|session| {
        session.borrow_mut().clear();
    });
}

fn require_unlocked(role: &str) -> Result<[u8; 32], &'static str> {
    FFI_SESSION.with(|session| {
        session
            .borrow()
            .get(role)
            .map(|bytes| **bytes)
            .ok_or("wallet is locked for this role")
    })
}

/// # Safety
/// `role`/`message` must each be a valid null-terminated UTF-8 C string
/// or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_sign_request_message(role: *const c_char, message: *const c_char) -> *mut c_char {
    let role = from_c_str(role);
    let message = from_c_str(message);
    let secret = match require_unlocked(&role) {
        Ok(s) => s,
        Err(e) => return err_json(e),
    };
    match signing::sign_personal_message(&secret, &message) {
        Ok(sig) => ok_json(&sig),
        Err(e) => err_json(e),
    }
}

/// See `signing::sign_hex_digest`'s doc comment for why this is a
/// distinct function from `ffi_sign_request_message`, not
/// interchangeable with it.
///
/// # Safety
/// `role`/`digest_hex` must each be a valid null-terminated UTF-8 C
/// string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_sign_hex_digest(role: *const c_char, digest_hex: *const c_char) -> *mut c_char {
    let role = from_c_str(role);
    let digest_hex = from_c_str(digest_hex);
    let secret = match require_unlocked(&role) {
        Ok(s) => s,
        Err(e) => return err_json(e),
    };
    match signing::sign_hex_digest(&secret, &digest_hex) {
        Ok(sig) => ok_json(&sig),
        Err(e) => err_json(e),
    }
}

/// # Safety
/// `role`/`unsigned_tx_json` must each be a valid null-terminated UTF-8
/// C string or null.
#[no_mangle]
pub unsafe extern "C" fn ffi_sign_transaction(role: *const c_char, unsigned_tx_json: *const c_char) -> *mut c_char {
    let role = from_c_str(role);
    let unsigned_tx_json = from_c_str(unsigned_tx_json);
    let secret = match require_unlocked(&role) {
        Ok(s) => s,
        Err(e) => return err_json(e),
    };
    match signing::sign_eip1559_transaction(&secret, &unsigned_tx_json) {
        Ok(tx) => ok_json(&tx),
        Err(e) => err_json(e),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    unsafe fn cstr(s: &str) -> CString {
        CString::new(s).unwrap()
    }

    #[derive(serde::Deserialize)]
    struct ParsedEnvelope {
        ok: bool,
        value: Option<String>,
        #[allow(dead_code)]
        error: Option<String>,
    }

    unsafe fn parse(ptr: *mut c_char) -> ParsedEnvelope {
        let json = CStr::from_ptr(ptr).to_str().unwrap().to_string();
        let parsed: ParsedEnvelope = serde_json::from_str(&json).unwrap();
        ffi_free_string(ptr);
        parsed
    }

    #[test]
    fn generate_validate_and_derive_round_trip() {
        unsafe {
            let mnemonic_result = parse(ffi_generate_mnemonic(12));
            assert!(mnemonic_result.ok);
            let phrase = mnemonic_result.value.unwrap();
            assert_eq!(phrase.split_whitespace().count(), 12);

            let phrase_c = cstr(&phrase);
            assert_eq!(ffi_validate_mnemonic(phrase_c.as_ptr()), 1);

            let addr_result = parse(ffi_derive_preview_address(phrase_c.as_ptr()));
            assert!(addr_result.ok);
            assert!(addr_result.value.unwrap().starts_with("0x"));
        }
    }

    #[test]
    fn encrypt_unlock_sign_and_lock_round_trip() {
        unsafe {
            let phrase_result = parse(ffi_generate_mnemonic(12));
            let phrase = cstr(&phrase_result.value.unwrap());
            let role = cstr("signer");
            let password = cstr("correcthorsebatterystaple");

            let vault_result = parse(ffi_encrypt_vault(role.as_ptr(), phrase.as_ptr(), password.as_ptr(), 0.0));
            assert!(vault_result.ok);
            let vault_json = cstr(&vault_result.value.unwrap());

            assert_eq!(ffi_is_unlocked(role.as_ptr()), 0);
            let unlock_result = parse(ffi_unlock(role.as_ptr(), vault_json.as_ptr(), password.as_ptr()));
            assert!(unlock_result.ok);
            let address = unlock_result.value.unwrap();
            assert!(address.starts_with("0x"));
            assert_eq!(ffi_is_unlocked(role.as_ptr()), 1);

            let digest = cstr("0x1122334455667788990011223344556677889900112233445566778899aabb");
            let sig_result = parse(ffi_sign_hex_digest(role.as_ptr(), digest.as_ptr()));
            assert!(sig_result.ok);
            assert_eq!(sig_result.value.unwrap().len(), 132); // 0x + 130 hex chars

            let message = cstr("example.com wants you to sign in");
            let msg_sig_result = parse(ffi_sign_request_message(role.as_ptr(), message.as_ptr()));
            assert!(msg_sig_result.ok);

            ffi_lock(role.as_ptr());
            assert_eq!(ffi_is_unlocked(role.as_ptr()), 0);

            let after_lock = parse(ffi_sign_hex_digest(role.as_ptr(), digest.as_ptr()));
            assert!(!after_lock.ok);
        }
    }

    #[test]
    fn wrong_password_fails_to_unlock() {
        unsafe {
            let phrase_result = parse(ffi_generate_mnemonic(12));
            let phrase = cstr(&phrase_result.value.unwrap());
            let role = cstr("signer");
            let password = cstr("correcthorsebatterystaple");
            let wrong_password = cstr("wrongpassword");

            let vault_result = parse(ffi_encrypt_vault(role.as_ptr(), phrase.as_ptr(), password.as_ptr(), 0.0));
            let vault_json = cstr(&vault_result.value.unwrap());

            let unlock_result = parse(ffi_unlock(role.as_ptr(), vault_json.as_ptr(), wrong_password.as_ptr()));
            assert!(!unlock_result.ok);
        }
    }
}
