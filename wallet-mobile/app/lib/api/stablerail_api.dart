// Mirrors wallet-web's api/stablerailApi.ts - client for wallet-backend's
// stablerail component (PLAN.md §18/19): Stablerail-backed NGN bank
// onboarding and onramp (cNGN). Every route requires the wallet's own
// signed request.
import 'http_client.dart';

class Bank {
  Bank({required this.code, required this.name});
  final String code;
  final String name;

  factory Bank.fromJson(Map<String, dynamic> json) => Bank(code: json['code'] as String, name: json['name'] as String);
}

class StablerailApi {
  StablerailApi(this._client);
  final ApiClient _client;

  Future<List<Bank>> listBanks(String walletAddress) {
    return _client.request(
      '/v1/stablerail/banks',
      walletAddress: walletAddress,
      decode: (json) => (json as List).map((e) => Bank.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<String> initiateOnboarding(String walletAddress, String bvn) {
    return _client.request(
      '/v1/stablerail/onboard/${Uri.encodeComponent(bvn)}',
      method: 'POST',
      walletAddress: walletAddress,
      decode: (json) => (json as Map<String, dynamic>)['message'] as String,
    );
  }

  Future<void> initiateOnramp(String walletAddress, num amount) {
    return _client.request<void>(
      '/v1/stablerail/onramp/$amount',
      method: 'POST',
      walletAddress: walletAddress,
      decode: (_) {},
    );
  }
}
