// Mirrors wallet-web's api/sharedAccessApi.ts. Every route is
// SignatureAuth-protected but, unlike other domains, always authenticates
// the caller as *themselves* (their own primary wallet) rather than as
// the group being acted on - the target group is always named by its own
// :groupId path/body param instead, so every call here passes the
// caller's own primaryAddress as walletAddress.
import 'http_client.dart';

// The original's exact three-tier permission model: APPROVER is the only
// role that becomes an on-chain Safe owner and whose signature can
// satisfy the group's threshold; INITIATOR may propose actions but not
// approve them; VIEW_ONLY may only view balances/history. There is no
// combined role - a member who needs both simply holds APPROVER.
enum GroupRole { initiator, approver, viewOnly }

extension GroupRoleJson on GroupRole {
  String toJson() {
    switch (this) {
      case GroupRole.initiator:
        return 'INITIATOR';
      case GroupRole.approver:
        return 'APPROVER';
      case GroupRole.viewOnly:
        return 'VIEW_ONLY';
    }
  }

  static GroupRole fromJson(String value) {
    switch (value) {
      case 'INITIATOR':
        return GroupRole.initiator;
      case 'APPROVER':
        return GroupRole.approver;
      default:
        return GroupRole.viewOnly;
    }
  }

  String get label {
    switch (this) {
      case GroupRole.initiator:
        return 'Initiator';
      case GroupRole.approver:
        return 'Approver';
      case GroupRole.viewOnly:
        return 'View only';
    }
  }
}

class ClosedGroup {
  ClosedGroup({required this.id, required this.name, required this.purpose, this.address, this.threshold, required this.disabled, required this.createdAt});

  final int id;
  final String name;
  final String purpose;
  final String? address;
  final int? threshold;
  final bool disabled;
  final String createdAt;

  factory ClosedGroup.fromJson(Map<String, dynamic> json) => ClosedGroup(
        id: json['id'] as int,
        name: json['name'] as String,
        purpose: json['purpose'] as String,
        address: json['address'] as String?,
        threshold: json['threshold'] as int?,
        disabled: json['disabled'] as bool? ?? false,
        createdAt: json['createdAt'] as String? ?? '',
      );
}

class GroupMember {
  GroupMember({required this.id, required this.groupId, required this.memberAddress, required this.role, required this.createdAt});

  final int id;
  final int groupId;
  final String memberAddress;
  final GroupRole role;
  final String createdAt;

  factory GroupMember.fromJson(Map<String, dynamic> json) => GroupMember(
        id: json['id'] as int,
        groupId: json['groupId'] as int,
        memberAddress: json['memberAddress'] as String,
        role: GroupRoleJson.fromJson(json['role'] as String),
        createdAt: json['createdAt'] as String? ?? '',
      );
}

class WalletSummary {
  WalletSummary({
    required this.address,
    required this.kind,
    required this.role,
    required this.threshold,
    required this.disabled,
    this.groupId,
    this.name,
    required this.isOwner,
    required this.isShared,
  });

  final String address;
  final String kind; // 'primary' | 'group'
  final String role;
  final int threshold;
  final bool disabled;
  final int? groupId;
  final String? name;
  // isOwner: true for the primary wallet, or a group this caller created.
  // false for a group someone else created and added this caller to -
  // "shared with me". isShared: true when the underlying wallet has more
  // than one member.
  final bool isOwner;
  final bool isShared;

  factory WalletSummary.fromJson(Map<String, dynamic> json) => WalletSummary(
        address: json['address'] as String,
        kind: json['kind'] as String,
        role: json['role'] as String,
        threshold: json['threshold'] as int? ?? 0,
        disabled: json['disabled'] as bool? ?? false,
        groupId: json['groupId'] as int?,
        name: json['name'] as String?,
        isOwner: json['isOwner'] as bool? ?? false,
        isShared: json['isShared'] as bool? ?? false,
      );
}

class CuratedBalance {
  CuratedBalance({required this.symbol, required this.contractAddress, required this.decimals, required this.balance});

  final String symbol;
  final String contractAddress;
  final int decimals;
  final String balance;

  factory CuratedBalance.fromJson(Map<String, dynamic> json) => CuratedBalance(
        symbol: json['symbol'] as String,
        contractAddress: json['contractAddress'] as String,
        decimals: json['decimals'] as int? ?? 0,
        balance: json['balance'] as String? ?? '0',
      );
}

class PendingAction {
  PendingAction({
    required this.id,
    required this.groupId,
    required this.proposerAddress,
    required this.kind,
    required this.description,
    required this.to,
    required this.tokenAddress,
    required this.value,
    required this.data,
    required this.requiredApprovals,
    required this.status,
    required this.txHash,
    this.rejectionReason,
  });

  final int id;
  final int groupId;
  final String proposerAddress;
  final String kind;
  final String description;
  final String to;
  final String tokenAddress;
  final String value;
  final String data;
  final int requiredApprovals;
  final String status;
  final String txHash;
  final String? rejectionReason;

  factory PendingAction.fromJson(Map<String, dynamic> json) => PendingAction(
        id: json['id'] as int,
        groupId: json['groupId'] as int,
        proposerAddress: json['proposerAddress'] as String? ?? '',
        kind: json['kind'] as String? ?? '',
        description: json['description'] as String? ?? '',
        to: json['to'] as String? ?? '',
        tokenAddress: json['tokenAddress'] as String? ?? '',
        value: json['value'] as String? ?? '',
        data: json['data'] as String? ?? '',
        requiredApprovals: json['requiredApprovals'] as int? ?? 0,
        status: json['status'] as String? ?? '',
        txHash: json['txHash'] as String? ?? '',
        rejectionReason: json['rejectionReason'] as String?,
      );
}

class ActionDetail extends PendingAction {
  ActionDetail({required PendingAction action, required this.digestToSign})
      : super(
          id: action.id,
          groupId: action.groupId,
          proposerAddress: action.proposerAddress,
          kind: action.kind,
          description: action.description,
          to: action.to,
          tokenAddress: action.tokenAddress,
          value: action.value,
          data: action.data,
          requiredApprovals: action.requiredApprovals,
          status: action.status,
          txHash: action.txHash,
          rejectionReason: action.rejectionReason,
        );

  final String digestToSign;

  factory ActionDetail.fromJson(Map<String, dynamic> json) =>
      ActionDetail(action: PendingAction.fromJson(json), digestToSign: json['digestToSign'] as String);
}

class SharedAccessApi {
  SharedAccessApi(this._client);
  final ApiClient _client;

  Future<List<WalletSummary>> listWallets(String primaryAddress) {
    return _client.request(
      '/v1/shared-access/wallets',
      walletAddress: primaryAddress,
      decode: (json) => (json as List).map((e) => WalletSummary.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  /// Members are named by username, never an address the caller would
  /// have to already know - resolved server-side.
  Future<ClosedGroup> createGroup(
    String primaryAddress,
    String name,
    int threshold,
    List<({String username, GroupRole role})> members,
  ) {
    return _client.request(
      '/v1/shared-access/groups',
      method: 'POST',
      walletAddress: primaryAddress,
      body: {
        'name': name,
        'threshold': threshold,
        'members': members.map((m) => {'username': m.username, 'role': m.role.toJson()}).toList(),
      },
      decode: (json) => ClosedGroup.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<ClosedGroup> getGroup(String primaryAddress, int groupId) {
    return _client.request(
      '/v1/shared-access/groups/$groupId',
      walletAddress: primaryAddress,
      decode: (json) => ClosedGroup.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<List<GroupMember>> listMembers(String primaryAddress, int groupId) {
    return _client.request(
      '/v1/shared-access/groups/$groupId/members',
      walletAddress: primaryAddress,
      decode: (json) => (json as List).map((e) => GroupMember.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<String> getBalance(String primaryAddress, int groupId, {String? tokenAddress}) async {
    final query = tokenAddress != null ? '?token=$tokenAddress' : '';
    final result = await _client.request(
      '/v1/shared-access/balance/$groupId$query',
      walletAddress: primaryAddress,
      decode: (json) => json as Map<String, dynamic>,
    );
    return result['balance'] as String? ?? '0';
  }

  Future<List<CuratedBalance>> getCuratedBalances(String primaryAddress, int groupId) {
    return _client.request(
      '/v1/shared-access/balance/$groupId/curated',
      walletAddress: primaryAddress,
      decode: (json) => (json as List).map((e) => CuratedBalance.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<PendingAction> proposePayment(
    String primaryAddress,
    int groupId,
    String description,
    String recipient,
    String amount, {
    String? tokenAddress,
  }) {
    return _client.request(
      '/v1/shared-access/actions',
      method: 'POST',
      walletAddress: primaryAddress,
      body: {
        'groupId': groupId,
        'kind': 'payment',
        'description': description,
        'recipient': recipient,
        'amount': amount,
        'tokenAddress': tokenAddress ?? '',
      },
      decode: (json) => PendingAction.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<List<PendingAction>> listPending(String primaryAddress) {
    return _client.request(
      '/v1/shared-access/actions',
      walletAddress: primaryAddress,
      decode: (json) => (json as List).map((e) => PendingAction.fromJson(e as Map<String, dynamic>)).toList(),
    );
  }

  Future<ActionDetail> getAction(String primaryAddress, int actionId) {
    return _client.request(
      '/v1/shared-access/actions/$actionId',
      walletAddress: primaryAddress,
      decode: (json) => ActionDetail.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PendingAction> approveAction(String primaryAddress, int actionId, String signature) {
    return _client.request(
      '/v1/shared-access/actions/$actionId/approve',
      method: 'POST',
      walletAddress: primaryAddress,
      body: {'signature': signature},
      decode: (json) => PendingAction.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PendingAction> rejectAction(String primaryAddress, int actionId, String reason) {
    return _client.request(
      '/v1/shared-access/actions/$actionId/reject',
      method: 'POST',
      walletAddress: primaryAddress,
      body: {'reason': reason},
      decode: (json) => PendingAction.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PendingAction> proposeAddMember(String primaryAddress, int groupId, String username, GroupRole role, int newThreshold) {
    return _client.request(
      '/v1/shared-access/groups/$groupId/members',
      method: 'POST',
      walletAddress: primaryAddress,
      body: {'username': username, 'role': role.toJson(), 'newThreshold': newThreshold},
      decode: (json) => PendingAction.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PendingAction> proposeRemoveMember(String primaryAddress, int groupId, String username, int newThreshold) {
    return _client.request(
      '/v1/shared-access/groups/$groupId/members/$username/remove',
      method: 'POST',
      walletAddress: primaryAddress,
      body: {'newThreshold': newThreshold},
      decode: (json) => PendingAction.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PendingAction> proposeChangeThreshold(String primaryAddress, int groupId, int newThreshold) {
    return _client.request(
      '/v1/shared-access/groups/$groupId/threshold',
      method: 'POST',
      walletAddress: primaryAddress,
      body: {'newThreshold': newThreshold},
      decode: (json) => PendingAction.fromJson(json as Map<String, dynamic>),
    );
  }

  Future<PendingAction> proposeDisableGroup(String primaryAddress, int groupId) {
    return _client.request(
      '/v1/shared-access/groups/$groupId/disable',
      method: 'POST',
      walletAddress: primaryAddress,
      decode: (json) => PendingAction.fromJson(json as Map<String, dynamic>),
    );
  }
}
