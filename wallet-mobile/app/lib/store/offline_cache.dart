// A separate, non-secret cache from the encrypted vault - the mobile
// counterpart to wallet-web's cache/offlineCache.ts (there: IndexedDB;
// here: SharedPreferences, both a plain non-secret key-value store).
// Used for the primary wallet's Safe address (public on-chain, not
// sensitive - see cacheKeys.primaryWalletAddress's reasoning there) so
// app-start hydration can resolve it offline without a live GET
// /v1/users/me round trip every launch.
import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';

class CacheEntry {
  CacheEntry(this.data, this.fetchedAtMs);
  final String data;
  final int fetchedAtMs;

  Map<String, dynamic> toJson() => {'data': data, 'fetchedAt': fetchedAtMs};
  factory CacheEntry.fromJson(Map<String, dynamic> json) => CacheEntry(json['data'] as String, json['fetchedAt'] as int);
}

String primaryWalletAddressKey(String signerAddress) => 'cache:primaryWalletAddress:$signerAddress';

class OfflineCache {
  OfflineCache(this._prefs);

  final SharedPreferences _prefs;

  static Future<OfflineCache> load() async => OfflineCache(await SharedPreferences.getInstance());

  Future<void> setEntry(String key, String data) async {
    await _prefs.setString(key, jsonEncode(CacheEntry(data, DateTime.now().millisecondsSinceEpoch).toJson()));
  }

  CacheEntry? getEntry(String key) {
    final raw = _prefs.getString(key);
    if (raw == null) return null;
    return CacheEntry.fromJson(jsonDecode(raw) as Map<String, dynamic>);
  }
}
