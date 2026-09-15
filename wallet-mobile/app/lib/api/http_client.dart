// A thin fetch wrapper for wallet-backend's REST API - the mobile
// counterpart to wallet-web's api/httpClient.ts. Signing itself happens
// only in core/wallet_core_client.dart (PLAN.md §11): every authenticated
// request signs `fullPathWithQuery + signerAddress + timestamp` with the
// signer role's key and attaches the result as four headers - there is no
// session/login step or bearer token in this scheme.
import 'dart:convert';
import 'package:http/http.dart' as http;

import '../config/network.dart';
import '../core/wallet_core_client.dart';

class ApiError implements Exception {
  ApiError(this.status, this.message);
  final int status;
  final String message;
  @override
  String toString() => message;
}

class ApiClient {
  ApiClient({required this.network, required this.walletCore, http.Client? httpClient})
      : _http = httpClient ?? http.Client();

  final NetworkSettings network;
  final WalletCoreClient walletCore;
  final http.Client _http;

  Future<Map<String, String>> _signatureAuthHeaders(String path, String walletAddress) async {
    final signerAddress = await walletCore.getKnownAddress('signer');
    if (signerAddress == null) {
      throw ApiError(0, 'Signer wallet is locked or not set up - unlock it before making this request.');
    }
    final timestamp = (DateTime.now().millisecondsSinceEpoch ~/ 1000).toString();
    // Must match wallet-backend's own construction exactly:
    // c.Request.URL.RequestURI() + signer + timestampStr.
    final message = path + signerAddress + timestamp;
    final signature = await walletCore.signRequestMessage('signer', message);
    return {
      'X-Signer-Address': signerAddress,
      'X-Wallet-Address': walletAddress,
      'X-Signature': signature,
      'X-Timestamp': timestamp,
    };
  }

  /// [walletAddress]: the wallet this request acts on (X-Wallet-Address) -
  /// required for every SignatureAuth-protected route, omitted (null) for
  /// public ones. Always signed with the **signer** role's key regardless
  /// of which wallet [walletAddress] names.
  Future<T> request<T>(
    String path, {
    String method = 'GET',
    Map<String, dynamic>? body,
    String? walletAddress,
    required T Function(dynamic json) decode,
  }) async {
    final headers = {'Content-Type': 'application/json'};
    if (walletAddress != null) {
      headers.addAll(await _signatureAuthHeaders(path, walletAddress));
    }

    final uri = Uri.parse('${network.getNetworkConfig().backendUrl}$path');
    http.Response response;
    try {
      switch (method) {
        case 'POST':
          response = await _http.post(uri, headers: headers, body: body != null ? jsonEncode(body) : null);
        case 'DELETE':
          response = await _http.delete(uri, headers: headers, body: body != null ? jsonEncode(body) : null);
        default:
          response = await _http.get(uri, headers: headers);
      }
    } catch (_) {
      throw ApiError(0, 'Network request failed - check your connection');
    }

    final text = response.body;
    final decoded = text.isNotEmpty ? jsonDecode(text) : null;

    if (response.statusCode < 200 || response.statusCode >= 300) {
      String message = 'Request failed with status ${response.statusCode}';
      if (decoded is Map<String, dynamic>) {
        message = (decoded['message'] ?? decoded['error'] ?? message) as String;
      }
      throw ApiError(response.statusCode, message);
    }
    return decode(decoded);
  }
}
