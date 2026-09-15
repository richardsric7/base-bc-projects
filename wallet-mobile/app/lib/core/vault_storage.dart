// Persists VaultRecord JSON (wallet-core's encrypted-at-rest blob - the
// ciphertext only, never a decrypted key) to the OS Keychain/Keystore via
// flutter_secure_storage - the mobile counterpart to wallet-web's
// worker/vaultStorage.ts (IndexedDB). One record per role ('signer' is
// the only role that ever gets a real vault - PLAN.md §3/§4 correct the
// original two-mnemonic model the same way wallet-web's did).
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

class VaultStorage {
  VaultStorage({FlutterSecureStorage? storage}) : _storage = storage ?? const FlutterSecureStorage();

  final FlutterSecureStorage _storage;

  String _recordKey(String role) => 'vault:$role';
  String _addressKey(String role) => 'vault:$role:address';

  Future<void> save(String role, String vaultRecordJson, String address) async {
    await _storage.write(key: _recordKey(role), value: vaultRecordJson);
    await _storage.write(key: _addressKey(role), value: address);
  }

  Future<String?> readRecord(String role) => _storage.read(key: _recordKey(role));

  Future<String?> readAddress(String role) => _storage.read(key: _addressKey(role));

  Future<bool> hasVault(String role) async {
    final record = await readRecord(role);
    return record != null;
  }

  Future<void> delete(String role) async {
    await _storage.delete(key: _recordKey(role));
    await _storage.delete(key: _addressKey(role));
  }
}
