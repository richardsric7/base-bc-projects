// Shared test scaffolding: an in-memory FlutterSecureStoragePlatform (no
// real Keychain/Keystore available on this Linux test host) and a helper
// to build a fully-wired AppServices against the real compiled
// wallet-core .so (WALLET_CORE_LIB_PATH), matching how
// core/wallet_core_client_test.dart verifies the FFI bridge itself.
import 'package:flutter_secure_storage_platform_interface/flutter_secure_storage_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:wallet_mobile/app_services.dart';

class FakeSecureStoragePlatform extends FlutterSecureStoragePlatform {
  final Map<String, String> values = {};

  @override
  Future<void> write({required String key, required String? value, required Map<String, String> options}) async {
    if (value == null) {
      values.remove(key);
    } else {
      values[key] = value;
    }
  }

  @override
  Future<String?> read({required String key, required Map<String, String> options}) async => values[key];

  @override
  Future<void> delete({required String key, required Map<String, String> options}) async {
    values.remove(key);
  }

  @override
  Future<bool> containsKey({required String key, required Map<String, String> options}) async => values.containsKey(key);

  @override
  Future<Map<String, String>> readAll({required Map<String, String> options}) async => Map.of(values);

  @override
  Future<void> deleteAll({required Map<String, String> options}) async => values.clear();
}

Future<AppServices> buildTestServices() async {
  FlutterSecureStoragePlatform.instance = FakeSecureStoragePlatform();
  SharedPreferences.setMockInitialValues({});
  return AppServices.create();
}
