// Runtime testnet/mainnet switch - the mobile counterpart to wallet-web's
// config/network.ts. Same reasoning: wallet-backend is deployed once per
// network (separate services, separate databases), so "switching
// network" means pointing this app at a different wallet-backend
// deployment, never translating in-memory state between them.
//
// Vite's build-time VITE_* env vars become Dart's compile-time
// `--dart-define` values (String.fromEnvironment) - baked into the build
// the same way, read at compile time rather than runtime. The active
// choice itself is a runtime toggle in SharedPreferences, matching
// wallet-web's localStorage.
import 'package:shared_preferences/shared_preferences.dart';

enum NetworkEnv { testnet, mainnet }

class NetworkConfig {
  const NetworkConfig({required this.env, required this.label, required this.backendUrl, required this.chainId});

  final NetworkEnv env;
  final String label;
  final String backendUrl;
  final int chainId;

  bool get isConfigured => backendUrl.isNotEmpty;
}

const _storageKey = 'trovo.activeNetwork';

const _networks = <NetworkEnv, NetworkConfig>{
  NetworkEnv.testnet: NetworkConfig(
    env: NetworkEnv.testnet,
    label: 'Base Sepolia (Testnet)',
    backendUrl: String.fromEnvironment('WALLET_BACKEND_URL_TESTNET', defaultValue: 'http://localhost:8080'),
    chainId: int.fromEnvironment('CHAIN_ID_TESTNET', defaultValue: 84532),
  ),
  NetworkEnv.mainnet: NetworkConfig(
    env: NetworkEnv.mainnet,
    label: 'Base Mainnet',
    backendUrl: String.fromEnvironment('WALLET_BACKEND_URL_MAINNET', defaultValue: ''),
    chainId: int.fromEnvironment('CHAIN_ID_MAINNET', defaultValue: 8453),
  ),
};

const _defaultNetwork =
    String.fromEnvironment('DEFAULT_NETWORK', defaultValue: 'testnet') == 'mainnet' ? NetworkEnv.mainnet : NetworkEnv.testnet;

class NetworkSettings {
  NetworkSettings(this._prefs);

  final SharedPreferences _prefs;

  static Future<NetworkSettings> load() async => NetworkSettings(await SharedPreferences.getInstance());

  NetworkEnv getActiveNetwork() {
    final stored = _prefs.getString(_storageKey);
    if (stored == 'mainnet') return NetworkEnv.mainnet;
    if (stored == 'testnet') return NetworkEnv.testnet;
    return _defaultNetwork;
  }

  NetworkConfig getNetworkConfig([NetworkEnv? network]) => _networks[network ?? getActiveNetwork()]!;

  bool isNetworkConfigured(NetworkEnv network) => _networks[network]!.isConfigured;

  /// Persists the chosen network. Unlike wallet-web (which reloads the
  /// whole page - the only way a browser tab can safely re-initialize
  /// from scratch), the caller here is expected to pop back to a fresh
  /// root widget itself (e.g. re-run the app's root MaterialApp) after
  /// this returns, for the same reason: testnet and mainnet are separate
  /// backends with nothing in memory worth carrying across.
  Future<void> switchActiveNetwork(NetworkEnv network) async {
    await _prefs.setString(_storageKey, network.name);
  }
}
