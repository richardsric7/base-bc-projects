// Mirrors wallet-web's api/fiatApi.ts - client for wallet-backend's fiat
// component (PLAN.md §18/19): Flutterwave-backed fiat top-up. Every route
// requires the wallet's own signed request, same as payments/assets.
import 'http_client.dart';

class ActivationQuote {
  ActivationQuote({required this.amount, required this.currency});
  final num amount;
  final String currency;

  factory ActivationQuote.fromJson(Map<String, dynamic> json) =>
      ActivationQuote(amount: json['amount'] as num, currency: json['currency'] as String);
}

class FiatInvoice {
  FiatInvoice({
    required this.id,
    required this.reference,
    required this.paymentType,
    required this.amount,
    required this.currency,
    required this.status,
    required this.createdAt,
  });

  final String id;
  final String reference;
  final String paymentType;
  final num amount;
  final String currency;
  final String status;
  final String createdAt;

  factory FiatInvoice.fromJson(Map<String, dynamic> json) => FiatInvoice(
        id: json['id'] as String,
        reference: json['reference'] as String,
        paymentType: json['paymentType'] as String,
        amount: json['amount'] as num,
        currency: json['currency'] as String,
        status: json['status'] as String,
        createdAt: json['createdAt'] as String,
      );
}

class FiatApi {
  FiatApi(this._client);
  final ApiClient _client;

  Future<ActivationQuote> getActivationQuote(String walletAddress) {
    return _client.request(
      '/v1/fiat/activate',
      walletAddress: walletAddress,
      decode: (json) => ActivationQuote.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<List<FiatInvoice>> getFiatInvoices(String walletAddress) {
    return _client.request(
      '/v1/fiat/payments',
      walletAddress: walletAddress,
      decode: (json) => ((json as Map<String, dynamic>)['invoices'] as List)
          .map((e) => FiatInvoice.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }

  Future<FiatInvoice> createFiatInvoice(
    String walletAddress,
    String reference,
    String paymentType,
    num amount,
    String currency,
  ) {
    return _client.request(
      '/v1/fiat/flutterwave/invoices',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'reference': reference, 'paymentType': paymentType, 'amount': amount, 'currency': currency},
      decode: (json) => FiatInvoice.fromJson(json as Map<String, dynamic>),
    );
  }
}
