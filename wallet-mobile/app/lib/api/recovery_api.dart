// Mirrors wallet-web's api/recoveryApi.ts - wallet-backend PLAN.md §15:
// two independent, coexisting recovery branches - Branch A (free, DB-only
// address swap, abandons funds/sub-wallets/shared-access at the old
// address) and Branch B (paid, true wallet-signer recovery via a Safe
// owner swap, preserves everything). A user may have neither, either, or
// both enabled.
import 'http_client.dart';
import 'users_api.dart';

class SecurityQuestion {
  SecurityQuestion({required this.id, required this.question});
  final int id;
  final String question;

  factory SecurityQuestion.fromJson(Map<String, dynamic> json) =>
      SecurityQuestion(id: json['id'] as int, question: json['question'] as String);
}

class SecurityAnswerInput {
  SecurityAnswerInput({required this.securityQuestionId, required this.answer});
  final int securityQuestionId;
  final String answer;

  Map<String, dynamic> toJson() => {'securityQuestionId': securityQuestionId, 'answer': answer};
}

class UnsignedTx {
  UnsignedTx({
    required this.chainId,
    required this.nonce,
    this.to,
    required this.value,
    required this.data,
    required this.gas,
    required this.maxFeePerGas,
    required this.maxPriorityFeePerGas,
    required this.type,
  });

  final String chainId;
  final int nonce;
  final String? to;
  final String value;
  final String data;
  final int gas;
  final String maxFeePerGas;
  final String maxPriorityFeePerGas;
  final String type;

  factory UnsignedTx.fromJson(Map<String, dynamic> json) => UnsignedTx(
        chainId: json['chainId'] as String,
        nonce: json['nonce'] as int,
        to: json['to'] as String?,
        value: json['value'] as String,
        data: json['data'] as String,
        gas: json['gas'] as int,
        maxFeePerGas: json['maxFeePerGas'] as String,
        maxPriorityFeePerGas: json['maxPriorityFeePerGas'] as String,
        type: json['type'] as String,
      );
}

class EnableWalletRecoveryChallenge {
  EnableWalletRecoveryChallenge({
    required this.addOwnerSafeTxHash,
    required this.setGuardSafeTxHash,
    this.feeTx,
    this.feeWalletAddress,
    this.feeAmountWei,
  });

  final String addOwnerSafeTxHash;
  final String setGuardSafeTxHash;
  final UnsignedTx? feeTx;
  final String? feeWalletAddress;
  final String? feeAmountWei;

  factory EnableWalletRecoveryChallenge.fromJson(Map<String, dynamic> json) => EnableWalletRecoveryChallenge(
        addOwnerSafeTxHash: json['addOwnerSafeTxHash'] as String,
        setGuardSafeTxHash: json['setGuardSafeTxHash'] as String,
        feeTx: json['feeTx'] == null ? null : UnsignedTx.fromJson(json['feeTx'] as Map<String, dynamic>),
        feeWalletAddress: json['feeWalletAddress'] as String?,
        feeAmountWei: json['feeAmountWei'] as String?,
      );
}

class DisableWalletRecoveryChallenge {
  DisableWalletRecoveryChallenge({required this.removeOwnerSafeTxHash, required this.clearGuardSafeTxHash});
  final String removeOwnerSafeTxHash;
  final String clearGuardSafeTxHash;

  factory DisableWalletRecoveryChallenge.fromJson(Map<String, dynamic> json) => DisableWalletRecoveryChallenge(
        removeOwnerSafeTxHash: json['removeOwnerSafeTxHash'] as String,
        clearGuardSafeTxHash: json['clearGuardSafeTxHash'] as String,
      );
}

class AccountRecoveryLog {
  AccountRecoveryLog({required this.oldAddress, required this.newAddress, required this.createdAt});
  final String oldAddress;
  final String newAddress;
  final String createdAt;

  factory AccountRecoveryLog.fromJson(Map<String, dynamic> json) => AccountRecoveryLog(
        oldAddress: json['oldAddress'] as String,
        newAddress: json['newAddress'] as String,
        createdAt: json['createdAt'] as String,
      );
}

class WalletRecoveryLog {
  WalletRecoveryLog({
    required this.walletAddress,
    required this.oldSignerAddress,
    required this.newSignerAddress,
    required this.txHash,
    required this.createdAt,
  });

  final String walletAddress;
  final String oldSignerAddress;
  final String newSignerAddress;
  final String txHash;
  final String createdAt;

  factory WalletRecoveryLog.fromJson(Map<String, dynamic> json) => WalletRecoveryLog(
        walletAddress: json['walletAddress'] as String,
        oldSignerAddress: json['oldSignerAddress'] as String,
        newSignerAddress: json['newSignerAddress'] as String,
        txHash: json['txHash'] as String,
        createdAt: json['createdAt'] as String,
      );
}

/// Must match wallet-backend's services.RecoveryMessage(username,
/// newAddress) exactly, byte-for-byte - this is a genuine human-composed
/// string message, signed with signRequestMessage (NOT signHexDigest).
/// Shared by both branches: Branch A signs it with the new *wallet*
/// address's key, Branch B with the new *signer* address's key, but the
/// message template itself is identical either way.
String buildRecoveryMessage(String username, String newAddress) =>
    'wallet-backend account recovery\nusername: $username\nnew address: $newAddress';

class RecoveryApi {
  RecoveryApi(this._client);
  final ApiClient _client;

  /// GET /v1/users/security-questions (public) - the fixed catalog to
  /// pick from when setting up account-recovery questions.
  Future<List<SecurityQuestion>> listSecurityQuestions() {
    return _client.request(
      '/v1/users/security-questions',
      decode: (json) => (json as List).map((e) => SecurityQuestion.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  /// POST /v1/users/security-answers (SignatureAuth) - stores one hashed
  /// answer. Call once per question the user picks (typically 2-3).
  Future<void> setSecurityAnswer(String walletAddress, int securityQuestionId, String answer) {
    return _client.request<void>(
      '/v1/users/security-answers',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'securityQuestionId': securityQuestionId, 'answer': answer},
      decode: (_) {},
    );
  }

  // --- Branch A: free, DB-only address swap (recovery.go) -------------

  Future<void> enableAccountRecovery(String walletAddress) {
    return _client.request<void>('/v1/users/account-recovery', method: 'POST', walletAddress: walletAddress, decode: (_) {});
  }

  Future<void> disableAccountRecovery(String walletAddress) {
    return _client.request<void>('/v1/users/account-recovery', method: 'DELETE', walletAddress: walletAddress, decode: (_) {});
  }

  /// POST /v1/account-recovery/:username/request-otp (public,
  /// unauthenticated by design - the whole point is helping someone who
  /// can no longer produce a signature at all). Always responds success
  /// regardless of whether the username exists or has recovery enabled,
  /// so this can never be used to probe for either.
  Future<void> requestAccountRecoveryOTP(String username) {
    return _client.request<void>(
      '/v1/account-recovery/${Uri.encodeComponent(username)}/request-otp',
      method: 'POST',
      decode: (_) {},
    );
  }

  /// POST /v1/account-recovery/:username/recover (public).
  /// [newAddressSignature] must be a personal_sign (signRequestMessage,
  /// NOT signHexDigest) produced by newAddress's own freshly-generated
  /// key, proving control of the address the account is being
  /// re-pointed to.
  Future<AccountRecoveryLog> recoverAccount(
    String username,
    String newAddress,
    String newAddressSignature,
    String otp,
    List<SecurityAnswerInput> answers,
  ) {
    return _client.request(
      '/v1/account-recovery/${Uri.encodeComponent(username)}/recover',
      method: 'POST',
      body: {
        'newAddress': newAddress,
        'newAddressSignature': newAddressSignature,
        'otp': otp,
        'answers': answers.map((a) => a.toJson()).toList(),
      },
      decode: (json) => AccountRecoveryLog.fromJson(json as Map<String, dynamic>),
    );
  }

  // --- Branch B: paid, true wallet-signer recovery (wallet_recovery.go) -

  Future<EnableWalletRecoveryChallenge> buildEnableWalletRecovery(String walletAddress) {
    return _client.request(
      '/v1/users/wallet-recovery/enable',
      method: 'POST',
      walletAddress: walletAddress,
      decode: (json) => EnableWalletRecoveryChallenge.fromJson(json as Map<String, dynamic>),
    );
  }

  /// [addOwnerSignature]/[setGuardSignature] must be signHexDigest (NOT
  /// signRequestMessage) over the two SafeTxHash values - these are
  /// binary digests, not human-composed strings.
  Future<User> confirmEnableWalletRecovery(
    String walletAddress,
    String feeTxHash,
    String addOwnerSignature,
    String setGuardSignature,
  ) {
    return _client.request(
      '/v1/users/wallet-recovery/enable/confirm',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'feeTxHash': feeTxHash, 'addOwnerSignature': addOwnerSignature, 'setGuardSignature': setGuardSignature},
      decode: (json) => User.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<DisableWalletRecoveryChallenge> buildDisableWalletRecovery(String walletAddress) {
    return _client.request(
      '/v1/users/wallet-recovery/disable',
      method: 'POST',
      walletAddress: walletAddress,
      decode: (json) => DisableWalletRecoveryChallenge.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<User> confirmDisableWalletRecovery(String walletAddress, String removeOwnerSignature, String clearGuardSignature) {
    return _client.request(
      '/v1/users/wallet-recovery/disable/confirm',
      method: 'POST',
      walletAddress: walletAddress,
      body: {'removeOwnerSignature': removeOwnerSignature, 'clearGuardSignature': clearGuardSignature},
      decode: (json) => User.fromJson(json as Map<String, dynamic>),
    );
  }

  /// POST /v1/account-recovery/:username/recover-wallet (public).
  /// [newSignerAddressSignature] must be signRequestMessage (the same
  /// RecoveryMessage shape Branch A uses) produced by newSignerAddress's
  /// own freshly-generated key.
  Future<WalletRecoveryLog> recoverWallet(
    String username,
    String newSignerAddress,
    String newSignerAddressSignature,
    String otp,
    List<SecurityAnswerInput> answers,
  ) {
    return _client.request(
      '/v1/account-recovery/${Uri.encodeComponent(username)}/recover-wallet',
      method: 'POST',
      body: {
        'newSignerAddress': newSignerAddress,
        'newSignerAddressSignature': newSignerAddressSignature,
        'otp': otp,
        'answers': answers.map((a) => a.toJson()).toList(),
      },
      decode: (json) => WalletRecoveryLog.fromJson(json as Map<String, dynamic>),
    );
  }
}
