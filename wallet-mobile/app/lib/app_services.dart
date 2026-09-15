// Bundles this app's singletons (wallet-core client, API clients, network
// settings, offline cache) - constructed once at startup and handed down
// via provider, the same role wallet-web's various singleton client
// modules play implicitly through ES module caching.
import 'core/wallet_core_client.dart';
import 'config/network.dart';
import 'store/offline_cache.dart';
import 'api/http_client.dart';
import 'api/users_api.dart';
import 'api/assets_api.dart';
import 'api/payments_api.dart';

class AppServices {
  AppServices._({
    required this.walletCore,
    required this.network,
    required this.cache,
    required this.api,
    required this.users,
    required this.assets,
    required this.payments,
  });

  final WalletCoreClient walletCore;
  final NetworkSettings network;
  final OfflineCache cache;
  final ApiClient api;
  final UsersApi users;
  final AssetsApi assets;
  final PaymentsApi payments;

  static Future<AppServices> create() async {
    final walletCore = WalletCoreClient();
    final network = await NetworkSettings.load();
    final cache = await OfflineCache.load();
    final api = ApiClient(network: network, walletCore: walletCore);
    return AppServices._(
      walletCore: walletCore,
      network: network,
      cache: cache,
      api: api,
      users: UsersApi(api),
      assets: AssetsApi(api),
      payments: PaymentsApi(api),
    );
  }
}
