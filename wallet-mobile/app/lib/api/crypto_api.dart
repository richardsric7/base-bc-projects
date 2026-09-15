// Mirrors wallet-web's api/cryptoApi.ts - client for wallet-backend's
// crypto component (PLAN.md §18/19): OneLiquidity-backed external-crypto
// deposit/withdrawal. Withdrawal is a real Safe transaction (the treasury
// debit) - build -> personal_sign digest -> confirm, same pattern as
// payments_api.dart, per wallet-backend PLAN.md §18's fix.
import 'http_client.dart';

class DepositAddress {
  DepositAddress({required this.address, required this.network});
  final String address;
  final String network;

  factory DepositAddress.fromJson(Map<String, dynamic> json) =>
      DepositAddress(address: json['address'] as String, network: json['network'] as String);
}

class WithdrawalNetwork {
  WithdrawalNetwork({required this.network, required this.withdrawMin, required this.withdrawMax, required this.withdrawFee});
  final String network;
  final String withdrawMin;
  final String withdrawMax;
  final String withdrawFee;

  factory WithdrawalNetwork.fromJson(Map<String, dynamic> json) => WithdrawalNetwork(
        network: json['network'] as String,
        withdrawMin: json['withdrawMin'] as String,
        withdrawMax: json['withdrawMax'] as String,
        withdrawFee: json['withdrawFee'] as String,
      );
}

class WithdrawalNetworksResult {
  WithdrawalNetworksResult({required this.networks, required this.treasuryAddress});
  final List<WithdrawalNetwork> networks;
  final String treasuryAddress;

  factory WithdrawalNetworksResult.fromJson(Map<String, dynamic> json) => WithdrawalNetworksResult(
        networks: (json['networks'] as List).map((e) => WithdrawalNetwork.fromJson(e as Map<String, dynamic>)).toList(),
        treasuryAddress: json['treasuryAddress'] as String,
      );
}

class WithdrawalProposal {
  WithdrawalProposal({required this.actionId, required this.digestToSign});
  final int actionId;
  final String digestToSign;

  factory WithdrawalProposal.fromJson(Map<String, dynamic> json) =>
      WithdrawalProposal(actionId: json['actionId'] as int, digestToSign: json['digestToSign'] as String);
}

class WithdrawalRequest {
  WithdrawalRequest({
    required this.id,
    required this.currency,
    required this.network,
    required this.toAddress,
    required this.amountSubmitted,
    required this.amountToWithdraw,
    required this.status,
    required this.treasuryTxHash,
    required this.createdAt,
  });

  final String id;
  final String currency;
  final String network;
  final String toAddress;
  final num amountSubmitted;
  final num amountToWithdraw;
  final String status;
  final String treasuryTxHash;
  final String createdAt;

  factory WithdrawalRequest.fromJson(Map<String, dynamic> json) => WithdrawalRequest(
        id: json['id'] as String,
        currency: json['currency'] as String,
        network: json['network'] as String,
        toAddress: json['toAddress'] as String,
        amountSubmitted: json['amountSubmitted'] as num,
        amountToWithdraw: json['amountToWithdraw'] as num,
        status: json['status'] as String,
        treasuryTxHash: json['treasuryTxHash'] as String,
        createdAt: json['createdAt'] as String,
      );
}

class CryptoApi {
  CryptoApi(this._client);
  final ApiClient _client;

  Future<List<DepositAddress>> getDepositAddresses(String walletAddress, String currency) {
    return _client.request(
      '/v1/crypto/deposit-address/${Uri.encodeComponent(currency)}',
      walletAddress: walletAddress,
      decode: (json) => (json as List).map((e) => DepositAddress.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<WithdrawalNetworksResult> getWithdrawalNetworks(String walletAddress, String currency) {
    return _client.request(
      '/v1/crypto/withdrawal-networks/${Uri.encodeComponent(currency)}',
      walletAddress: walletAddress,
      decode: (json) => WithdrawalNetworksResult.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<WithdrawalProposal> buildWithdrawal(String walletAddress, String currency, String network, num amount) {
    return _client.request(
      '/v1/crypto/withdrawals/build',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'currency': currency, 'network': network, 'amount': amount},
      decode: (json) => WithdrawalProposal.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<WithdrawalRequest> confirmWithdrawal(
    String walletAddress,
    String currency,
    String network,
    String toAddress,
    num amount,
    int actionId,
    String signature,
  ) {
    return _client.request(
      '/v1/crypto/withdrawals/confirm',
      method: 'POST',
      walletAddress: walletAddress,
      body: {
        'currency': currency,
        'network': network,
        'toAddress': toAddress,
        'amount': amount,
        'actionId': actionId,
        'signature': signature,
      },
      decode: (json) => WithdrawalRequest.fromJson(json as Map<String, dynamic>),
    );
  }
}
