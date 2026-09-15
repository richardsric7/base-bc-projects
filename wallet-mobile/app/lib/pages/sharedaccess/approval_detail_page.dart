// Mirrors wallet-web's pages/sharedaccess/ApprovalDetail.tsx - view one
// PendingAction and either approve it (personal_sign over the exact
// digest getAction returns) or reject it with a reason.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../api/sharedaccess_api.dart';

enum _Status { idle, signing, submitting, done }

class ApprovalDetailPage extends StatefulWidget {
  const ApprovalDetailPage({super.key, required this.actionId});
  final int actionId;

  @override
  State<ApprovalDetailPage> createState() => _ApprovalDetailPageState();
}

class _ApprovalDetailPageState extends State<ApprovalDetailPage> {
  ActionDetail? _action;
  String _error = '';
  String _reason = '';
  _Status _status = _Status.idle;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    services.sharedAccess
        .getAction(primaryAddress, widget.actionId)
        .then((a) => setState(() => _action = a))
        .catchError((err) => setState(() => _error = err.toString()));
  }

  Future<void> _approve() async {
    final services = context.read<AppServices>();
    final wallet = context.read<WalletState>();
    final primaryAddress = wallet.primary.address;
    final action = _action;
    if (primaryAddress == null || action == null) return;
    setState(() => _error = '');
    try {
      setState(() => _status = _Status.signing);
      final signature = await services.walletCore.signHexDigest('signer', action.digestToSign);
      setState(() => _status = _Status.submitting);
      await services.sharedAccess.approveAction(primaryAddress, widget.actionId, signature);
      _load();
      setState(() => _status = _Status.done);
    } catch (err) {
      setState(() {
        _error = err.toString();
        _status = _Status.idle;
      });
    }
  }

  Future<void> _reject() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null || _reason.isEmpty) return;
    setState(() => _error = '');
    try {
      setState(() => _status = _Status.submitting);
      await services.sharedAccess.rejectAction(primaryAddress, widget.actionId, _reason);
      _load();
      setState(() => _status = _Status.done);
    } catch (err) {
      setState(() {
        _error = err.toString();
        _status = _Status.idle;
      });
    }
  }

  String _statusLabel() {
    switch (_status) {
      case _Status.signing:
        return 'Signing…';
      case _Status.submitting:
        return 'Submitting…';
      default:
        return 'Approve';
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_error.isNotEmpty) {
      return Scaffold(appBar: AppBar(title: const Text('Review action')), body: Padding(padding: const EdgeInsets.all(16), child: Text(_error, style: const TextStyle(color: Colors.red))));
    }
    final action = _action;
    if (action == null) {
      return Scaffold(appBar: AppBar(title: const Text('Review action')), body: const Padding(padding: EdgeInsets.all(16), child: Text('Loading…')));
    }

    final busy = _status == _Status.signing || _status == _Status.submitting;
    final terminal = action.status == 'EXECUTED' || action.status == 'REJECTED';

    return Scaffold(
      appBar: AppBar(title: const Text('Review action')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Card(
            child: Padding(
              padding: const EdgeInsets.all(12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Kind: ${action.kind.replaceAll('_', ' ')}'),
                  Text('Description: ${action.description.isNotEmpty ? action.description : '-'}'),
                  Text('To: ${action.to}'),
                  Text('Value: ${action.value}'),
                  Text('Status: ${action.status}'),
                  Text('Required approvals: ${action.requiredApprovals}'),
                  if (action.txHash.isNotEmpty) Text('Tx hash: ${action.txHash}'),
                  if (action.rejectionReason != null) Text('Rejected: ${action.rejectionReason}', style: const TextStyle(color: Colors.red)),
                ],
              ),
            ),
          ),
          const SizedBox(height: 16),
          if (!terminal) ...[
            AppButton(label: busy ? _statusLabel() : 'Approve', onPressed: busy ? null : _approve),
            const SizedBox(height: 16),
            AppTextInput(label: 'Rejection reason', value: _reason, hint: 'Why reject this?', onChanged: (v) => setState(() => _reason = v)),
            const SizedBox(height: 8),
            TextButton(
              onPressed: busy || _reason.isEmpty ? null : _reject,
              child: const Text('Reject', style: TextStyle(color: Colors.red)),
            ),
          ],
        ],
      ),
    );
  }
}
