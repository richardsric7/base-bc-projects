// Mirrors wallet-web's pages/sharedaccess/GroupDetail.tsx - the
// original's dashboard/sharedAccess/walletInfo.tsx and update.tsx, folded
// into one page. Member-management proposals (add/remove member, change
// threshold, disable group) go through the exact same propose/approve
// pipeline as a payment.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../api/sharedaccess_api.dart';

class GroupDetailPage extends StatefulWidget {
  const GroupDetailPage({super.key, required this.groupId});
  final int groupId;

  @override
  State<GroupDetailPage> createState() => _GroupDetailPageState();
}

class _GroupDetailPageState extends State<GroupDetailPage> {
  ClosedGroup? _group;
  List<GroupMember> _members = [];
  String _nativeBalance = '';
  List<CuratedBalance> _curated = [];
  String _error = '';

  @override
  void initState() {
    super.initState();
    _reload();
  }

  void _reload() {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    Future.wait([
      services.sharedAccess.getGroup(primaryAddress, widget.groupId),
      services.sharedAccess.listMembers(primaryAddress, widget.groupId),
      services.sharedAccess.getBalance(primaryAddress, widget.groupId),
      services.sharedAccess.getCuratedBalances(primaryAddress, widget.groupId).catchError((_) => <CuratedBalance>[]),
    ]).then((results) {
      setState(() {
        _group = results[0] as ClosedGroup;
        _members = results[1] as List<GroupMember>;
        _nativeBalance = results[2] as String;
        _curated = results[3] as List<CuratedBalance>;
      });
    }).catchError((err) => setState(() => _error = err.toString()));
  }

  // Proposing and immediately self-approving is only meaningful for a
  // 1-of-1 group (a plain sub-wallet) or when this member alone can
  // already satisfy the threshold; for a genuine multi-party group the
  // proposal still needs the other members' own approvals via
  // approvals_page.dart regardless.
  Future<void> _proposeAndSelfApprove(Future<PendingAction> Function() propose) async {
    final services = context.read<AppServices>();
    final wallet = context.read<WalletState>();
    final primaryAddress = wallet.primary.address;
    final signerAddress = wallet.signer.address;
    if (primaryAddress == null || signerAddress == null) return;
    setState(() => _error = '');
    try {
      final action = await propose();
      final detail = await services.sharedAccess.getAction(primaryAddress, action.id);
      final signature = await services.walletCore.signHexDigest('signer', detail.digestToSign);
      await services.sharedAccess.approveAction(primaryAddress, action.id, signature);
      _reload();
    } catch (err) {
      setState(() => _error = err.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_error.isNotEmpty) {
      return Scaffold(appBar: AppBar(title: const Text('Shared wallet')), body: Padding(padding: const EdgeInsets.all(16), child: Text(_error, style: const TextStyle(color: Colors.red))));
    }
    final group = _group;
    if (group == null) {
      return Scaffold(appBar: AppBar(title: const Text('Shared wallet')), body: const Padding(padding: EdgeInsets.all(16), child: Text('Loading…')));
    }

    return Scaffold(
      appBar: AppBar(title: Text(group.name)),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Card(
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Address: ${group.address}'),
                  Text('Threshold: ${group.threshold}'),
                  Text('Native balance: $_nativeBalance'),
                  if (group.disabled) const Text('Disabled', style: TextStyle(color: Colors.red)),
                ],
              ),
            ),
          ),
          if (_curated.isNotEmpty)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text('Curated token balances', style: TextStyle(fontWeight: FontWeight.bold)),
                    ..._curated.map((c) => Row(mainAxisAlignment: MainAxisAlignment.spaceBetween, children: [Text(c.symbol), Text(c.balance)])),
                  ],
                ),
              ),
            ),
          const SizedBox(height: 16),
          const Text('Members', style: TextStyle(fontWeight: FontWeight.bold)),
          ..._members.map((m) => ListTile(dense: true, title: Text(m.memberAddress), trailing: Text(m.role.label))),
          const SizedBox(height: 16),
          _ProposePaymentForm(
            disabled: group.disabled,
            onPropose: (recipient, amount, tokenAddress) => _proposeAndSelfApprove(
              () => context.read<AppServices>().sharedAccess.proposePayment(
                    context.read<WalletState>().primary.address!,
                    widget.groupId,
                    'shared-access payment',
                    recipient,
                    amount,
                    tokenAddress: tokenAddress,
                  ),
            ),
          ),
          const SizedBox(height: 16),
          _ManageMembersSection(
            disabled: group.disabled,
            onAddMember: (username, role, newThreshold) => _proposeAndSelfApprove(
              () => context.read<AppServices>().sharedAccess.proposeAddMember(
                    context.read<WalletState>().primary.address!,
                    widget.groupId,
                    username,
                    role,
                    newThreshold,
                  ),
            ),
            onRemoveMember: (username, newThreshold) => _proposeAndSelfApprove(
              () => context.read<AppServices>().sharedAccess.proposeRemoveMember(
                    context.read<WalletState>().primary.address!,
                    widget.groupId,
                    username,
                    newThreshold,
                  ),
            ),
            onChangeThreshold: (newThreshold) => _proposeAndSelfApprove(
              () => context.read<AppServices>().sharedAccess.proposeChangeThreshold(
                    context.read<WalletState>().primary.address!,
                    widget.groupId,
                    newThreshold,
                  ),
            ),
            onDisable: () => _proposeAndSelfApprove(
              () => context.read<AppServices>().sharedAccess.proposeDisableGroup(
                    context.read<WalletState>().primary.address!,
                    widget.groupId,
                  ),
            ),
          ),
        ],
      ),
    );
  }
}

class _ProposePaymentForm extends StatefulWidget {
  const _ProposePaymentForm({required this.disabled, required this.onPropose});
  final bool disabled;
  final Future<void> Function(String recipient, String amount, String? tokenAddress) onPropose;

  @override
  State<_ProposePaymentForm> createState() => _ProposePaymentFormState();
}

class _ProposePaymentFormState extends State<_ProposePaymentForm> {
  String _recipient = '';
  String _amount = '';
  bool _busy = false;

  Future<void> _submit() async {
    setState(() => _busy = true);
    await widget.onPropose(_recipient, _amount, null);
    setState(() {
      _busy = false;
      _recipient = '';
      _amount = '';
    });
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Propose a payment', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Recipient', value: _recipient, hint: '0x...', onChanged: (v) => _recipient = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Amount (base units)', value: _amount, hint: '1000000000000000000', onChanged: (v) => _amount = v),
        const SizedBox(height: 8),
        AppButton(
          label: _busy ? 'Proposing…' : 'Propose payment',
          onPressed: widget.disabled || _busy || _recipient.isEmpty || _amount.isEmpty ? null : _submit,
        ),
      ],
    );
  }
}

class _ManageMembersSection extends StatefulWidget {
  const _ManageMembersSection({
    required this.disabled,
    required this.onAddMember,
    required this.onRemoveMember,
    required this.onChangeThreshold,
    required this.onDisable,
  });

  final bool disabled;
  final Future<void> Function(String username, GroupRole role, int newThreshold) onAddMember;
  final Future<void> Function(String username, int newThreshold) onRemoveMember;
  final Future<void> Function(int newThreshold) onChangeThreshold;
  final Future<void> Function() onDisable;

  @override
  State<_ManageMembersSection> createState() => _ManageMembersSectionState();
}

class _ManageMembersSectionState extends State<_ManageMembersSection> {
  String _newUsername = '';
  GroupRole _newRole = GroupRole.approver;
  String _removeUsername = '';
  String _threshold = '';
  bool _busy = false;

  Future<void> _run(Future<void> Function() fn) async {
    setState(() => _busy = true);
    try {
      await fn();
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final threshold = int.tryParse(_threshold);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Manage group', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Add member username', value: _newUsername, hint: 'username', onChanged: (v) => _newUsername = v),
        const SizedBox(height: 4),
        DropdownButton<GroupRole>(
          value: _newRole,
          items: GroupRole.values.map((r) => DropdownMenuItem(value: r, child: Text(r.label))).toList(),
          onChanged: (r) => setState(() => _newRole = r ?? _newRole),
        ),
        AppTextInput(label: 'New threshold after adding', value: _threshold, hint: '2', onChanged: (v) => setState(() => _threshold = v)),
        const SizedBox(height: 8),
        AppButton(
          label: 'Propose add member',
          onPressed: widget.disabled || _busy || _newUsername.isEmpty || threshold == null
              ? null
              : () => _run(() => widget.onAddMember(_newUsername, _newRole, threshold)),
        ),
        const SizedBox(height: 16),
        AppTextInput(label: 'Remove member username', value: _removeUsername, hint: 'username', onChanged: (v) => _removeUsername = v),
        const SizedBox(height: 8),
        AppButton(
          label: 'Propose remove member',
          onPressed: widget.disabled || _busy || _removeUsername.isEmpty || threshold == null
              ? null
              : () => _run(() => widget.onRemoveMember(_removeUsername, threshold)),
        ),
        const SizedBox(height: 16),
        AppButton(
          label: 'Propose threshold change',
          onPressed: widget.disabled || _busy || threshold == null ? null : () => _run(() => widget.onChangeThreshold(threshold)),
        ),
        const SizedBox(height: 8),
        TextButton(
          onPressed: widget.disabled || _busy ? null : () => _run(widget.onDisable),
          child: const Text('Propose disabling this group', style: TextStyle(color: Colors.red)),
        ),
      ],
    );
  }
}
