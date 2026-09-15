// Mirrors wallet-web's api/assetsApi.ts.
import 'http_client.dart';

class CuratedToken {
  CuratedToken({required this.id, required this.symbol, required this.contractAddress, required this.decimals, required this.name});

  final int id;
  final String symbol;
  final String contractAddress;
  final int decimals;
  final String name;

  factory CuratedToken.fromJson(Map<String, dynamic> json) => CuratedToken(
        id: json['id'] as int,
        symbol: json['symbol'] as String,
        contractAddress: json['contractAddress'] as String,
        decimals: json['decimals'] as int,
        name: json['name'] as String,
      );
}

class AssetsApi {
  AssetsApi(this._client);
  final ApiClient _client;

  /// GET /v1/assets (public).
  Future<List<CuratedToken>> listCuratedTokens() {
    return _client.request(
      '/v1/assets',
      decode: (json) => (json as List).map((e) => CuratedToken.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  /// GET /v1/assets/balance/:address?token=... (public). Omitted/empty
  /// tokenAddress = native ETH balance.
  Future<String> getBalance(String address, {String? tokenAddress}) {
    final query = tokenAddress != null ? '?token=${Uri.encodeComponent(tokenAddress)}' : '';
    return _client.request(
      '/v1/assets/balance/$address$query',
      decode: (json) => (json as Map<String, dynamic>)['balance'] as String,
    );
  }
}
