// Raw dart:ffi bindings to wallet-core's native C ABI (wallet-web's
// wallet-core/src/ffi.rs) - the mobile counterpart to wallet-web's
// worker/walletCoreWorker.ts + wasm-pkg glue. This is the *only* file in
// this app that touches dart:ffi directly (PLAN.md §4.1/§4.2's boundary,
// carried over from wallet-web) - everything else goes through
// WalletCoreClient (wallet_core_client.dart) instead.
import 'dart:ffi';
import 'dart:io';
import 'package:ffi/ffi.dart';

typedef GenerateMnemonicNative = Pointer<Utf8> Function(Uint8 wordCount);
typedef GenerateMnemonicDart = Pointer<Utf8> Function(int wordCount);

typedef ValidateMnemonicNative = Uint8 Function(Pointer<Utf8> phrase);
typedef ValidateMnemonicDart = int Function(Pointer<Utf8> phrase);

typedef DerivePreviewAddressNative = Pointer<Utf8> Function(Pointer<Utf8> phrase);
typedef DerivePreviewAddressDart = Pointer<Utf8> Function(Pointer<Utf8> phrase);

typedef EncryptVaultNative = Pointer<Utf8> Function(
  Pointer<Utf8> role,
  Pointer<Utf8> phrase,
  Pointer<Utf8> password,
  Double createdAtMs,
);
typedef EncryptVaultDart = Pointer<Utf8> Function(
  Pointer<Utf8> role,
  Pointer<Utf8> phrase,
  Pointer<Utf8> password,
  double createdAtMs,
);

typedef UnlockNative = Pointer<Utf8> Function(
  Pointer<Utf8> role,
  Pointer<Utf8> vaultRecordJson,
  Pointer<Utf8> password,
);
typedef UnlockDart = Pointer<Utf8> Function(
  Pointer<Utf8> role,
  Pointer<Utf8> vaultRecordJson,
  Pointer<Utf8> password,
);

typedef IsUnlockedNative = Uint8 Function(Pointer<Utf8> role);
typedef IsUnlockedDart = int Function(Pointer<Utf8> role);

typedef LockNative = Void Function(Pointer<Utf8> role);
typedef LockDart = void Function(Pointer<Utf8> role);

typedef LockAllNative = Void Function();
typedef LockAllDart = void Function();

typedef SignRequestMessageNative = Pointer<Utf8> Function(Pointer<Utf8> role, Pointer<Utf8> message);
typedef SignRequestMessageDart = Pointer<Utf8> Function(Pointer<Utf8> role, Pointer<Utf8> message);

typedef SignHexDigestNative = Pointer<Utf8> Function(Pointer<Utf8> role, Pointer<Utf8> digestHex);
typedef SignHexDigestDart = Pointer<Utf8> Function(Pointer<Utf8> role, Pointer<Utf8> digestHex);

typedef SignTransactionNative = Pointer<Utf8> Function(Pointer<Utf8> role, Pointer<Utf8> unsignedTxJson);
typedef SignTransactionDart = Pointer<Utf8> Function(Pointer<Utf8> role, Pointer<Utf8> unsignedTxJson);

typedef FreeStringNative = Void Function(Pointer<Utf8> s);
typedef FreeStringDart = void Function(Pointer<Utf8> s);

/// Loads and exposes wallet-core's native library. On Android the .so
/// ships in the APK's jniLibs and the dynamic linker finds it by name
/// alone; on iOS it's statically linked into the app binary, reached via
/// `DynamicLibrary.process()`. For local Linux dev/test (this sandbox has
/// no Android/iOS toolchain to build against), `WALLET_CORE_LIB_PATH`
/// points at the Cargo-built .so directly - see wallet-mobile/README.md.
class WalletCoreBindings {
  WalletCoreBindings._(DynamicLibrary lib)
      : generateMnemonic = lib.lookupFunction<GenerateMnemonicNative, GenerateMnemonicDart>('ffi_generate_mnemonic'),
        validateMnemonic = lib.lookupFunction<ValidateMnemonicNative, ValidateMnemonicDart>('ffi_validate_mnemonic'),
        derivePreviewAddress =
            lib.lookupFunction<DerivePreviewAddressNative, DerivePreviewAddressDart>('ffi_derive_preview_address'),
        encryptVault = lib.lookupFunction<EncryptVaultNative, EncryptVaultDart>('ffi_encrypt_vault'),
        unlock = lib.lookupFunction<UnlockNative, UnlockDart>('ffi_unlock'),
        isUnlocked = lib.lookupFunction<IsUnlockedNative, IsUnlockedDart>('ffi_is_unlocked'),
        lock = lib.lookupFunction<LockNative, LockDart>('ffi_lock'),
        lockAll = lib.lookupFunction<LockAllNative, LockAllDart>('ffi_lock_all'),
        signRequestMessage =
            lib.lookupFunction<SignRequestMessageNative, SignRequestMessageDart>('ffi_sign_request_message'),
        signHexDigest = lib.lookupFunction<SignHexDigestNative, SignHexDigestDart>('ffi_sign_hex_digest'),
        signTransaction = lib.lookupFunction<SignTransactionNative, SignTransactionDart>('ffi_sign_transaction'),
        freeString = lib.lookupFunction<FreeStringNative, FreeStringDart>('ffi_free_string');

  final GenerateMnemonicDart generateMnemonic;
  final ValidateMnemonicDart validateMnemonic;
  final DerivePreviewAddressDart derivePreviewAddress;
  final EncryptVaultDart encryptVault;
  final UnlockDart unlock;
  final IsUnlockedDart isUnlocked;
  final LockDart lock;
  final LockAllDart lockAll;
  final SignRequestMessageDart signRequestMessage;
  final SignHexDigestDart signHexDigest;
  final SignTransactionDart signTransaction;
  final FreeStringDart freeString;

  static WalletCoreBindings? _instance;

  static WalletCoreBindings get instance => _instance ??= WalletCoreBindings._(_open());

  static DynamicLibrary _open() {
    final override = Platform.environment['WALLET_CORE_LIB_PATH'];
    if (override != null && override.isNotEmpty) {
      return DynamicLibrary.open(override);
    }
    if (Platform.isAndroid) return DynamicLibrary.open('libwallet_core.so');
    if (Platform.isIOS || Platform.isMacOS) return DynamicLibrary.process();
    if (Platform.isLinux) return DynamicLibrary.open('libwallet_core.so');
    if (Platform.isWindows) return DynamicLibrary.open('wallet_core.dll');
    throw UnsupportedError('wallet-core has no native build for this platform');
  }
}
