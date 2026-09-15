// Mirrors wallet-web's api/usersApi.ts exactly - same routes, same field
// names, same self-signed-registration and deploy-retry reasoning.
import 'http_client.dart';

class User {
  User({
    required this.id,
    required this.username,
    required this.email,
    required this.address,
    required this.signerAddress,
    required this.primaryWalletDeployed,
    required this.kycStatus,
    required this.accountRecoveryEnabled,
    required this.walletRecoveryEnabled,
  });

  final int id;
  final String username;
  final String email;
  // The primary wallet's Safe smart-contract address (wallet-backend
  // PLAN.md §13), computed and stored at registration time.
  final String address;
  final String signerAddress;
  final bool primaryWalletDeployed;
  final String kycStatus;
  final bool accountRecoveryEnabled;
  final bool walletRecoveryEnabled;

  factory User.fromJson(Map<String, dynamic> json) => User(
        id: json['id'] as int,
        username: json['username'] as String,
        email: json['email'] as String,
        address: json['address'] as String,
        signerAddress: json['signerAddress'] as String,
        primaryWalletDeployed: json['primaryWalletDeployed'] as bool? ?? false,
        kycStatus: json['kycStatus'] as String? ?? 'pending',
        accountRecoveryEnabled: json['accountRecoveryEnabled'] as bool? ?? false,
        walletRecoveryEnabled: json['walletRecoveryEnabled'] as bool? ?? false,
      );
}

class UsersApi {
  UsersApi(this._client);
  final ApiClient _client;

  /// POST /v1/users (SignatureAuth) - no wallet exists yet to name in
  /// X-Wallet-Address, so this is necessarily self-signed: pass the
  /// signer's own address as [signerAddress], used as both
  /// X-Signer-Address and X-Wallet-Address.
  Future<User> registerUser(String signerAddress, String username, String email) {
    return _client.request(
      '/v1/users',
      method: 'POST',
      walletAddress: signerAddress,
      body: {'username': username, 'email': email},
      decode: (json) => User.fromJson(json as Map<String, dynamic>),
    );
  }

  /// GET /v1/users/me (SignatureAuth, self-signed) - looks a profile up
  /// by the verified signer's own key. Used by onboarding (existing
  /// profile check) and app-start hydration (re-resolving the primary
  /// wallet address if the offline cache was cleared).
  Future<User> getMyUser(String signerAddress) {
    return _client.request(
      '/v1/users/me',
      walletAddress: signerAddress,
      decode: (json) => User.fromJson(json as Map<String, dynamic>),
    );
  }

  /// GET /v1/users/:username (public) - used by the recovery flow, which
  /// has no signer key to authenticate with at that point.
  Future<User> getUserByUsername(String username) {
    return _client.request(
      '/v1/users/${Uri.encodeComponent(username)}',
      decode: (json) => User.fromJson(json as Map<String, dynamic>),
    );
  }

  /// POST /v1/users/wallet/deploy (SignatureAuth, self-signed) - submits
  /// on-chain deployment of the caller's own primary wallet Safe.
  /// Idempotent and platform-fee-paid, so safe to call freely.
  Future<User> deployPrimaryWallet(String signerAddress) {
    return _client.request(
      '/v1/users/wallet/deploy',
      method: 'POST',
      walletAddress: signerAddress,
      decode: (json) => User.fromJson(json as Map<String, dynamic>),
    );
  }

  /// A payment/swap/approve `build` call 404s with this message when the
  /// wallet's Safe hasn't been deployed yet. Deploys and retries once.
  Future<T> withWalletDeployRetry<T>(String signerAddress, Future<T> Function() buildCall) async {
    try {
      return await buildCall();
    } on ApiError catch (err) {
      if (err.status == 404 && err.message.contains('shared-access group')) {
        await deployPrimaryWallet(signerAddress);
        return buildCall();
      }
      rethrow;
    }
  }
}
