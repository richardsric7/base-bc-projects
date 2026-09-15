// Mirrors wallet-web's api/paymentsApi.ts. Every wallet is a Safe with no
// private key of its own (wallet-backend PLAN.md §13/§17) - /build
// proposes the transfer as a real Safe transaction and returns the
// digest the caller's own signer key must sign (via
// WalletCoreClient.signHexDigest, NOT signRequestMessage) to approve it.
import 'http_client.dart';

class ActionProposal {
  ActionProposal({required this.actionId, required this.digestToSign, this.resolvedAddress});
  final int actionId;
  final String digestToSign;

  /// Only present on a payment proposal (wallet-backend's
  /// payments.PaymentProposal) - what `destination` actually resolved to,
  /// since it may be an address, username, email, or wallet alias
  /// (users.UserWallet's own doc comment). A swap proposal has no
  /// recipient to resolve, so this is always null there.
  final String? resolvedAddress;

  factory ActionProposal.fromJson(Map<String, dynamic> json) => ActionProposal(
        actionId: json['actionId'] as int,
        digestToSign: json['digestToSign'] as String,
        resolvedAddress: json['resolvedAddress'] as String?,
      );
}

class PaymentHistoryRecord {
  PaymentHistoryRecord({
    required this.idempotencyKey,
    required this.fromAddress,
    required this.toAddress,
    required this.tokenAddress,
    required this.amount,
    required this.txHash,
    required this.createdAt,
  });

  final String idempotencyKey;
  final String fromAddress;
  final String toAddress;
  final String tokenAddress;
  final String amount;
  final String txHash;
  final String createdAt;

  factory PaymentHistoryRecord.fromJson(Map<String, dynamic> json) => PaymentHistoryRecord(
        idempotencyKey: json['idempotencyKey'] as String,
        fromAddress: json['fromAddress'] as String,
        toAddress: json['toAddress'] as String,
        tokenAddress: json['tokenAddress'] as String,
        amount: json['amount'] as String,
        txHash: json['txHash'] as String,
        createdAt: json['createdAt'] as String,
      );
}

class PaymentsApi {
  PaymentsApi(this._client);
  final ApiClient _client;

  Future<ActionProposal> buildPayment(
    String walletAddress,
    String destination,
    String amount, {
    String? tokenAddress,
  }) {
    return _client.request(
      '/v1/payments/build',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'destination': destination, 'tokenAddress': tokenAddress ?? '', 'amount': amount},
      decode: (json) => ActionProposal.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PaymentHistoryRecord> submitPayment(
    String walletAddress,
    String idempotencyKey,
    int actionId,
    String signature,
    String destination,
    String amount, {
    String? tokenAddress,
  }) {
    return _client.request(
      '/v1/payments/submit',
      method: 'POST',
      walletAddress: walletAddress,
      body: {
        'idempotencyKey': idempotencyKey,
        'actionId': actionId,
        'signature': signature,
        'destination': destination,
        'tokenAddress': tokenAddress ?? '',
        'amount': amount,
      },
      decode: (json) => PaymentHistoryRecord.fromJson(json as Map<String, dynamic>),
    );
  }

  /// GET /v1/payments/history/:address is SignatureAuth-protected -
  /// [address] is both the history requested and the wallet the signer
  /// must own/have standing on.
  Future<List<PaymentHistoryRecord>> getPaymentHistory(String address) {
    return _client.request(
      '/v1/payments/history/$address',
      walletAddress: address,
      decode: (json) => (json as List).map((e) => PaymentHistoryRecord.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }
}
