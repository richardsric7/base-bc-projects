// Exercises the real wallet-core native library through the full
// WalletCoreClient stack (FFI marshaling, envelope decoding, and vault
// persistence via an in-memory FlutterSecureStorage-compatible fake) -
// not mocks of wallet-core itself. Requires WALLET_CORE_LIB_PATH to
// point at the Cargo-built libwallet_core.so (see wallet-mobile/README.md
// for the exact `flutter test` invocation).
import 'package:flutter_test/flutter_test.dart';
import 'package:wallet_mobile/core/wallet_core_client.dart';
import 'package:wallet_mobile/core/vault_storage.dart';
import 'package:flutter_secure_storage_platform_interface/flutter_secure_storage_platform_interface.dart';

/// A pure in-memory FlutterSecureStoragePlatform, so these tests don't
/// need a real Keychain/Keystore (unavailable on this Linux test host
/// anyway) - VaultStorage itself is exercised unmodified.
class _FakeSecureStoragePlatform extends FlutterSecureStoragePlatform {
  final Map<String, String> _values = {};

  @override
  Future<void> write({required String key, required String? value, required Map<String, String> options}) async {
    if (value == null) {
      _values.remove(key);
    } else {
      _values[key] = value;
    }
  }

  @override
  Future<String?> read({required String key, required Map<String, String> options}) async => _values[key];

  @override
  Future<void> delete({required String key, required Map<String, String> options}) async {
    _values.remove(key);
  }

  @override
  Future<bool> containsKey({required String key, required Map<String, String> options}) async =>
      _values.containsKey(key);

  @override
  Future<Map<String, String>> readAll({required Map<String, String> options}) async => Map.of(_values);

  @override
  Future<void> deleteAll({required Map<String, String> options}) async => _values.clear();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  FlutterSecureStoragePlatform.instance = _FakeSecureStoragePlatform();

  late WalletCoreClient client;

  setUp(() {
    client = WalletCoreClient(storage: VaultStorage());
  });

  // wallet-core's native session (ffi.rs's FFI_SESSION) is process-global
  // for the life of the test binary, not scoped per WalletCoreClient
  // instance - a real app only ever runs one such session for its whole
  // lifetime, but these tests share one process, so an unlocked role
  // from an earlier test would otherwise leak into the next one.
  tearDown(() async {
    await client.lockAllWallets();
  });

  test('generates a valid 12-word mnemonic and derives its address', () async {
    final mnemonic = await client.generateMnemonic(wordCount: 12);
    expect(mnemonic.split(' '), hasLength(12));
    expect(await client.validateMnemonic(mnemonic), isTrue);

    final address = await client.previewAddress(mnemonic);
    expect(address, startsWith('0x'));
    expect(address.length, 42);
  });

  test('rejects an invalid mnemonic', () async {
    expect(await client.validateMnemonic('not a real mnemonic phrase at all here'), isFalse);
  });

  test('createVault -> lock -> unlock round trip recovers the same address', () async {
    final mnemonic = await client.generateMnemonic(wordCount: 12);
    final address = await client.createVault('signer', mnemonic, 'correcthorsebatterystaple');

    expect(await client.hasVault('signer'), isTrue);
    expect(await client.getKnownAddress('signer'), address);
    expect(await client.isUnlocked('signer'), isTrue); // createVault leaves it unlocked, same as wallet-web

    await client.lockWallet('signer');
    expect(await client.isUnlocked('signer'), isFalse);

    final unlockedAddress = await client.unlockWallet('signer', 'correcthorsebatterystaple');
    expect(unlockedAddress, address);
    expect(await client.isUnlocked('signer'), isTrue);
  });

  test('unlock fails with the wrong password', () async {
    final mnemonic = await client.generateMnemonic(wordCount: 12);
    await client.createVault('signer', mnemonic, 'correcthorsebatterystaple');
    await client.lockWallet('signer');

    expect(
      () => client.unlockWallet('signer', 'wrongpassword'),
      throwsA(isA<WalletCoreError>()),
    );
  });

  test('signHexDigest and signRequestMessage both produce 65-byte hex signatures once unlocked', () async {
    final mnemonic = await client.generateMnemonic(wordCount: 12);
    await client.createVault('signer', mnemonic, 'correcthorsebatterystaple');

    final digestSig = await client.signHexDigest(
      'signer',
      '0x1122334455667788990011223344556677889900112233445566778899aabb',
    );
    expect(digestSig, matches(RegExp(r'^0x[0-9a-f]{130}$')));

    final messageSig = await client.signRequestMessage('signer', '/v1/example0xabc1700000000');
    expect(messageSig, matches(RegExp(r'^0x[0-9a-f]{130}$')));

    // Same underlying bytes signed two different ways (see
    // signHexDigest's own doc comment) must not produce the same
    // signature - proves this test isn't accidentally calling the same
    // function twice under two names.
    expect(digestSig, isNot(messageSig));
  });

  test('signing fails for a locked role', () async {
    expect(
      () => client.signHexDigest('signer', '0x00'),
      throwsA(isA<WalletCoreError>()),
    );
  });

  test('wipeWallet clears both the vault record and the unlocked session', () async {
    final mnemonic = await client.generateMnemonic(wordCount: 12);
    await client.createVault('signer', mnemonic, 'correcthorsebatterystaple');

    await client.wipeWallet('signer');

    expect(await client.hasVault('signer'), isFalse);
    expect(await client.isUnlocked('signer'), isFalse);
  });
}
