// Mirrors wallet-web's api/tokenizationApi.ts: wallet-backend's
// tokenization component (the asset-tokenization application/vetting/
// minting/sale pipeline, and the investor-facing purchase/interest/
// early-exit flows against a live sale). Every mutating on-chain step
// (application fee, crypto purchase, early exit) follows the same
// build -> personal_sign digest -> submit pattern as payments_api.dart/
// swaps_api.dart.
import 'http_client.dart';

class ActionProposal {
  ActionProposal({required this.actionId, required this.digestToSign});
  final int actionId;
  final String digestToSign;

  factory ActionProposal.fromJson(Map<String, dynamic> json) =>
      ActionProposal(actionId: json['actionId'] as int, digestToSign: json['digestToSign'] as String);
}

class PurchaseProposal extends ActionProposal {
  PurchaseProposal({required super.actionId, required super.digestToSign, this.paymentAmount});
  final String? paymentAmount;

  factory PurchaseProposal.fromJson(Map<String, dynamic> json) => PurchaseProposal(
        actionId: json['actionId'] as int,
        digestToSign: json['digestToSign'] as String,
        paymentAmount: json['paymentAmount'] as String?,
      );
}

/// Only the fields this app's UI actually reads/writes - the real backend
/// row carries hundreds of asset-class-specific columns; anything else
/// round-trips through [raw] untouched since this app never overwrites
/// fields it didn't set.
class TokenizedAsset {
  TokenizedAsset({
    required this.id,
    required this.status,
    required this.vettingStatus,
    required this.assetName,
    required this.assetCode,
    required this.assetDescription,
    required this.assetQuoteCurrency,
    required this.pricePerToken,
    required this.raw,
  });

  final int id;
  final String status;
  final bool vettingStatus;
  final String assetName;
  final String assetCode;
  final String assetDescription;
  final String assetQuoteCurrency;
  final String pricePerToken;
  final Map<String, dynamic> raw;

  factory TokenizedAsset.fromJson(Map<String, dynamic> json) => TokenizedAsset(
        id: json['id'] as int,
        status: json['status'] as String? ?? '',
        vettingStatus: json['vettingStatus'] as bool? ?? false,
        assetName: json['assetName'] as String? ?? '',
        assetCode: json['assetCode'] as String? ?? '',
        assetDescription: json['assetDescription'] as String? ?? '',
        assetQuoteCurrency: json['assetQuoteCurrency'] as String? ?? '',
        pricePerToken: json['pricePerToken'] as String? ?? '',
        raw: json,
      );
}

class NewApplicationInput {
  NewApplicationInput({
    required this.assetSector,
    required this.assetSubSector,
    required this.assetType,
    required this.assetName,
    required this.assetCode,
    required this.assetDescription,
    required this.assetCountryLocation,
    required this.assetQuoteCurrency,
    required this.numberOfTokenToBeIssued,
    required this.maxNumberOfTokenAvailableForSale,
    required this.pricePerToken,
    this.assetDecimals = 2,
  });

  final String assetSector;
  final String assetSubSector;
  final String assetType;
  final String assetName;
  final String assetCode;
  final String assetDescription;
  final String assetCountryLocation;
  final String assetQuoteCurrency;
  final String numberOfTokenToBeIssued;
  final String maxNumberOfTokenAvailableForSale;
  final String pricePerToken;
  final int assetDecimals;

  Map<String, dynamic> toJson() => {
        'assetSector': assetSector,
        'assetSubSector': assetSubSector,
        'assetType': assetType,
        'assetName': assetName,
        'assetCode': assetCode,
        'assetDescription': assetDescription,
        'assetCountryLocation': assetCountryLocation,
        'assetQuoteCurrency': assetQuoteCurrency,
        'numberOfTokenToBeIssued': numberOfTokenToBeIssued,
        'maxNumberOfTokenAvailableForSale': maxNumberOfTokenAvailableForSale,
        'pricePerToken': pricePerToken,
        'assetDecimals': assetDecimals,
      };
}

class TokenizedAssetSubscription {
  TokenizedAssetSubscription({
    required this.id,
    required this.tokenizedAssetId,
    required this.quantity,
    required this.paymentAmount,
    required this.paymentAssetSymbol,
    required this.channel,
  });

  final String id;
  final int tokenizedAssetId;
  final String quantity;
  final String paymentAmount;
  final String paymentAssetSymbol;
  final String channel;

  factory TokenizedAssetSubscription.fromJson(Map<String, dynamic> json) => TokenizedAssetSubscription(
        id: json['id'] as String,
        tokenizedAssetId: json['tokenizedAssetId'] as int,
        quantity: json['quantity'] as String? ?? '',
        paymentAmount: json['paymentAmount'] as String? ?? '',
        paymentAssetSymbol: json['paymentAssetSymbol'] as String? ?? '',
        channel: json['channel'] as String? ?? '',
      );
}

class ExpressionOfInterest {
  ExpressionOfInterest({required this.id, required this.tokenizedAssetId, this.amount});
  final int id;
  final int tokenizedAssetId;
  final String? amount;

  factory ExpressionOfInterest.fromJson(Map<String, dynamic> json) => ExpressionOfInterest(
        id: json['id'] as int,
        tokenizedAssetId: json['tokenizedAssetId'] as int,
        amount: json['amount'] as String?,
      );
}

class TokenizedAssetEarlyExit {
  TokenizedAssetEarlyExit({
    required this.id,
    required this.tokenizedAssetId,
    required this.tokenQuantityExited,
    required this.payoutCurrency,
    required this.estimatedPayoutAmount,
  });

  final int id;
  final int tokenizedAssetId;
  final String tokenQuantityExited;
  final String payoutCurrency;
  final String estimatedPayoutAmount;

  factory TokenizedAssetEarlyExit.fromJson(Map<String, dynamic> json) => TokenizedAssetEarlyExit(
        id: json['id'] as int,
        tokenizedAssetId: json['tokenizedAssetId'] as int,
        tokenQuantityExited: json['tokenQuantityExited'] as String? ?? '',
        payoutCurrency: json['payoutCurrency'] as String? ?? '',
        estimatedPayoutAmount: json['estimatedPayoutAmount'] as String? ?? '',
      );
}

class TokenizationApi {
  TokenizationApi(this._client);
  final ApiClient _client;

  Future<TokenizedAsset> submitApplication(String walletAddress, NewApplicationInput input) {
    return _client.request(
      '/v1/tokenization',
      method: 'POST',
      walletAddress: walletAddress,
      body: input.toJson(),
      decode: (json) => TokenizedAsset.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<List<TokenizedAsset>> listMyApplications(String walletAddress) {
    return _client.request(
      '/v1/tokenization/applications',
      walletAddress: walletAddress,
      decode: (json) => (json as List).map((e) => TokenizedAsset.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<TokenizedAsset> getApplication(String walletAddress, int assetId) {
    return _client.request(
      '/v1/tokenization/$assetId',
      walletAddress: walletAddress,
      decode: (json) => TokenizedAsset.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<void> deleteApplication(String walletAddress, int assetId) {
    return _client.request(
      '/v1/tokenization/$assetId',
      method: 'DELETE',
      walletAddress: walletAddress,
      decode: (_) {},
    );
  }

  /// Moves DRAFT -> APPLICATION_CONFIRMED and, if a fee is owed, returns a
  /// proposal for it; feePayment is null when no fee applies.
  Future<ActionProposal?> confirmApplication(String walletAddress, int assetId) async {
    final result = await _client.request(
      '/v1/tokenization/$assetId/confirm',
      method: 'PUT',
      walletAddress: walletAddress,
      decode: (json) => json as Map<String, dynamic>,
    );
    final feePayment = result['feePayment'] as Map<String, dynamic>?;
    return feePayment == null ? null : ActionProposal.fromJson(feePayment);
  }

  Future<void> submitApplicationFee(String walletAddress, int assetId, int actionId, String signature) {
    return _client.request(
      '/v1/tokenization/$assetId/confirm/pay-fee',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'actionId': actionId, 'signature': signature},
      decode: (_) {},
    );
  }

  Future<TokenizedAsset> confirmFeePayment(String walletAddress, int assetId) {
    return _client.request(
      '/v1/tokenization/$assetId/fee/confirm',
      method: 'POST',
      walletAddress: walletAddress,
      decode: (json) => TokenizedAsset.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PurchaseProposal> buildCryptoPurchase(String walletAddress, int assetId, String quantity) {
    return _client.request(
      '/v1/tokenization/$assetId/subscribe',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'quantity': quantity},
      decode: (json) => PurchaseProposal.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<TokenizedAssetSubscription> confirmCryptoPurchase(
    String walletAddress,
    int assetId,
    String quantity,
    int actionId,
    String signature,
  ) {
    return _client.request(
      '/v1/tokenization/$assetId/subscribe/confirm',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'quantity': quantity, 'actionId': actionId, 'signature': signature},
      decode: (json) => TokenizedAssetSubscription.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<List<TokenizedAssetSubscription>> listMySubscriptions(String walletAddress) {
    return _client.request(
      '/v1/tokenization/subscriptions/mine',
      walletAddress: walletAddress,
      decode: (json) => (json as List).map((e) => TokenizedAssetSubscription.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<ExpressionOfInterest> expressInterest(String walletAddress, int assetId, {String? amount}) {
    return _client.request(
      '/v1/tokenization/$assetId/interest',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'amount': amount},
      decode: (json) => ExpressionOfInterest.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<List<ExpressionOfInterest>> listMyInterest(String walletAddress) {
    return _client.request(
      '/v1/tokenization/interest/mine',
      walletAddress: walletAddress,
      decode: (json) => (json as List).map((e) => ExpressionOfInterest.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<PurchaseProposal> buildEarlyExit(String walletAddress, int assetId, String quantity) {
    return _client.request(
      '/v1/tokenization/$assetId/early-exit',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'quantity': quantity},
      decode: (json) => PurchaseProposal.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<TokenizedAssetEarlyExit> confirmEarlyExit(
    String walletAddress,
    int assetId,
    String quantity,
    int bankId,
    String accountNumber,
    String accountName,
    int actionId,
    String signature,
  ) {
    return _client.request(
      '/v1/tokenization/$assetId/early-exit/confirm',
      method: 'POST',
      walletAddress: walletAddress,
      body: {
        'quantity': quantity,
        'bankId': bankId,
        'accountNumber': accountNumber,
        'accountName': accountName,
        'actionId': actionId,
        'signature': signature,
      },
      decode: (json) => TokenizedAssetEarlyExit.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<List<TokenizedAssetEarlyExit>> listMyEarlyExits(String walletAddress) {
    return _client.request(
      '/v1/tokenization/early-exits/mine',
      walletAddress: walletAddress,
      decode: (json) => (json as List).map((e) => TokenizedAssetEarlyExit.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }
}
