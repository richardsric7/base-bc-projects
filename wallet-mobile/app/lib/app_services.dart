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
import 'api/swaps_api.dart';
import 'api/sharedaccess_api.dart';
import 'api/tokenization_api.dart';
import 'api/fiat_api.dart';
import 'api/stablerail_api.dart';
import 'api/crypto_api.dart';
import 'api/recovery_api.dart';

class AppServices {
  AppServices._({
    required this.walletCore,
    required this.network,
    required this.cache,
    required this.api,
    required this.users,
    required this.assets,
    required this.payments,
    required this.swaps,
    required this.sharedAccess,
    required this.tokenization,
    required this.fiat,
    required this.stablerail,
    required this.crypto,
    required this.recovery,
  });

  final WalletCoreClient walletCore;
  final NetworkSettings network;
  final OfflineCache cache;
  final ApiClient api;
  final UsersApi users;
  final AssetsApi assets;
  final PaymentsApi payments;
  final SwapsApi swaps;
  final SharedAccessApi sharedAccess;
  final TokenizationApi tokenization;
  final FiatApi fiat;
  final StablerailApi stablerail;
  final CryptoApi crypto;
  final RecoveryApi recovery;

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
      swaps: SwapsApi(api),
      sharedAccess: SharedAccessApi(api),
      tokenization: TokenizationApi(api),
      fiat: FiatApi(api),
      stablerail: StablerailApi(api),
      crypto: CryptoApi(api),
      recovery: RecoveryApi(api),
    );
  }
}
