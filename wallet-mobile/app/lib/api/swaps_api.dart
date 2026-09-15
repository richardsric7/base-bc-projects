// Mirrors wallet-web's api/swapsApi.ts. wallet-backend's swaps component
// is a generic DEX router-call builder, not an abstracted "swap A for B"
// - the caller supplies the router contract, its ABI, and which
// method/args to call. This app's swap page is responsible for knowing a
// specific router's ABI/method shape for whichever DEX it targets.
import 'http_client.dart';
import 'payments_api.dart' show ActionProposal;

class SwapsApi {
  SwapsApi(this._client);
  final ApiClient _client;

  Future<ActionProposal> buildSwap(
    String walletAddress, {
    required String routerAddress,
    required String routerAbi,
    required String method,
    List<dynamic>? args,
    String? valueWei,
  }) {
    return _client.request(
      '/v1/swaps/build',
      method: 'POST',
      walletAddress: walletAddress,
      body: {
        'routerAddress': routerAddress,
        'routerAbi': routerAbi,
        'method': method,
        'args': args ?? [],
        if (valueWei != null) 'valueWei': valueWei,
      },
      decode: (json) => ActionProposal.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<String> submitSwap(String walletAddress, int actionId, String signature) {
    return _client.request(
      '/v1/swaps/submit',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'actionId': actionId, 'signature': signature},
      decode: (json) => (json as Map<String, dynamic>)['hash'] as String,
    );
  }
}
