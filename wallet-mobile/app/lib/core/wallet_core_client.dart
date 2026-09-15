// The main-isolate-facing wallet-core API - the mobile counterpart to
// wallet-web's core/walletCoreClient.ts. Unlike wallet-web (which must
// hop through a Worker for isolation, since JS has no other way to keep
// a key out of a page's own execution context), a native FFI call here
// runs synchronously in-process - there is no separate "worker" isolate
// boundary to cross, so this file both marshals the FFI calls
// (via WalletCoreBindings) *and* persists VaultRecord JSON (via
// VaultStorage), combined into the same async surface wallet-web split
// across two files. The decrypted key itself still never leaves
// wallet-core's own native memory (WalletCoreBindings never exposes it) -
// only addresses and signatures cross back into Dart.
import 'dart:convert';
import 'dart:ffi';
import 'package:ffi/ffi.dart';

import 'wallet_core_bindings.dart';
import 'vault_storage.dart';

class WalletCoreError implements Exception {
  WalletCoreError(this.message);
  final String message;
  @override
  String toString() => message;
}

class _Envelope {
  _Envelope(this.ok, this.value, this.error);
  final bool ok;
  final String? value;
  final String? error;

  factory _Envelope.parse(String json) {
    final decoded = jsonDecode(json) as Map<String, dynamic>;
    return _Envelope(decoded['ok'] as bool, decoded['value'] as String?, decoded['error'] as String?);
  }
}

/// Wraps one `Pointer<Utf8> ffi_*(...)` call: builds native args, invokes
/// `call`, decodes the JSON envelope, frees the native string exactly
/// once, and either returns the value or throws.
String _callEnveloped(Pointer<Utf8> Function() call) {
  final resultPtr = call();
  try {
    final envelope = _Envelope.parse(resultPtr.toDartString());
    if (!envelope.ok) throw WalletCoreError(envelope.error ?? 'unknown wallet-core error');
    return envelope.value ?? '';
  } finally {
    WalletCoreBindings.instance.freeString(resultPtr);
  }
}

class WalletCoreClient {
  WalletCoreClient({VaultStorage? storage}) : _storage = storage ?? VaultStorage();

  final VaultStorage _storage;
  WalletCoreBindings get _bindings => WalletCoreBindings.instance;

  Future<String> generateMnemonic({int wordCount = 12}) async {
    return _callEnveloped(() => _bindings.generateMnemonic(wordCount));
  }

  Future<bool> validateMnemonic(String phrase) async {
    final phrasePtr = phrase.toNativeUtf8();
    try {
      return _bindings.validateMnemonic(phrasePtr) == 1;
    } finally {
      malloc.free(phrasePtr);
    }
  }

  Future<String> previewAddress(String phrase) async {
    final phrasePtr = phrase.toNativeUtf8();
    try {
      return _callEnveloped(() => _bindings.derivePreviewAddress(phrasePtr));
    } finally {
      malloc.free(phrasePtr);
    }
  }

  /// Encrypts `phrase` under `password`, persists the resulting
  /// VaultRecord (OS Keychain/Keystore via VaultStorage), and unlocks it
  /// immediately using the same password - so a freshly-created wallet is
  /// ready to sign right away (e.g. the registration request that
  /// follows signer creation) without asking for the same password twice
  /// in one flow. Mirrors wallet-web's worker CREATE_VAULT handler
  /// exactly (walletCoreWorker.ts), which combines the same two
  /// wasm-bindgen calls for the same reason.
  Future<String> createVault(String role, String phrase, String password) async {
    final rolePtr = role.toNativeUtf8();
    final phrasePtr = phrase.toNativeUtf8();
    final passwordPtr = password.toNativeUtf8();
    try {
      final recordJson = _callEnveloped(
        () => _bindings.encryptVault(rolePtr, phrasePtr, passwordPtr, DateTime.now().millisecondsSinceEpoch.toDouble()),
      );
      final address = (jsonDecode(recordJson) as Map<String, dynamic>)['address'] as String;
      await _storage.save(role, recordJson, address);

      final recordPtr = recordJson.toNativeUtf8();
      try {
        _callEnveloped(() => _bindings.unlock(rolePtr, recordPtr, passwordPtr));
      } finally {
        malloc.free(recordPtr);
      }
      return address;
    } finally {
      malloc.free(rolePtr);
      malloc.free(phrasePtr);
      malloc.free(passwordPtr);
    }
  }

  Future<String> unlockWallet(String role, String password) async {
    final recordJson = await _storage.readRecord(role);
    if (recordJson == null) throw WalletCoreError('no vault stored for role "$role"');

    final rolePtr = role.toNativeUtf8();
    final recordPtr = recordJson.toNativeUtf8();
    final passwordPtr = password.toNativeUtf8();
    try {
      return _callEnveloped(() => _bindings.unlock(rolePtr, recordPtr, passwordPtr));
    } finally {
      malloc.free(rolePtr);
      malloc.free(recordPtr);
      malloc.free(passwordPtr);
    }
  }

  Future<void> lockWallet(String role) async {
    final rolePtr = role.toNativeUtf8();
    try {
      _bindings.lock(rolePtr);
    } finally {
      malloc.free(rolePtr);
    }
  }

  Future<void> lockAllWallets() async {
    _bindings.lockAll();
  }

  Future<bool> isUnlocked(String role) async {
    final rolePtr = role.toNativeUtf8();
    try {
      return _bindings.isUnlocked(rolePtr) == 1;
    } finally {
      malloc.free(rolePtr);
    }
  }

  Future<bool> hasVault(String role) => _storage.hasVault(role);

  Future<String?> getKnownAddress(String role) => _storage.readAddress(role);

  /// Signs `message` (EIP-191 `personal_sign`) - a genuine human-composed
  /// string, e.g. SignatureAuth's `path + signer + timestamp` header
  /// message. **Not** interchangeable with signHexDigest - see that
  /// method's own doc comment.
  Future<String> signRequestMessage(String role, String message) async {
    final rolePtr = role.toNativeUtf8();
    final messagePtr = message.toNativeUtf8();
    try {
      return _callEnveloped(() => _bindings.signRequestMessage(rolePtr, messagePtr));
    } finally {
      malloc.free(rolePtr);
      malloc.free(messagePtr);
    }
  }

  /// Signs a `0x`-prefixed hex digest (a Safe transaction hash -
  /// wallet-backend's `digestToSign`/`*SafeTxHash` fields, PLAN.md
  /// §13/§15/§17) over its **raw decoded bytes**, matching
  /// wallet-backend's `cryptoutil.VerifyPersonalSignBytes` exactly - see
  /// wallet-web's wallet-core `signing::sign_hex_digest` doc comment for
  /// the real, previously-shipped bug this distinction fixes.
  /// `signRequestMessage` would hash the ASCII hex string instead and
  /// produce a signature that silently never verifies.
  Future<String> signHexDigest(String role, String digestHex) async {
    final rolePtr = role.toNativeUtf8();
    final digestPtr = digestHex.toNativeUtf8();
    try {
      return _callEnveloped(() => _bindings.signHexDigest(rolePtr, digestPtr));
    } finally {
      malloc.free(rolePtr);
      malloc.free(digestPtr);
    }
  }

  Future<String> signTransaction(String role, String unsignedTxJson) async {
    final rolePtr = role.toNativeUtf8();
    final txPtr = unsignedTxJson.toNativeUtf8();
    try {
      return _callEnveloped(() => _bindings.signTransaction(rolePtr, txPtr));
    } finally {
      malloc.free(rolePtr);
      malloc.free(txPtr);
    }
  }

  Future<void> wipeWallet(String role) async {
    await lockWallet(role);
    await _storage.delete(role);
  }
}
