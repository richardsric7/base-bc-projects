// Mirrors wallet-web's pages/sharedaccess/Approvals.tsx - every group the
// caller belongs to, in one queue.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../api/sharedaccess_api.dart';

class ApprovalsPage extends StatefulWidget {
  const ApprovalsPage({super.key});

  @override
  State<ApprovalsPage> createState() => _ApprovalsPageState();
}

class _ApprovalsPageState extends State<ApprovalsPage> {
  List<PendingAction>? _actions;
  String _error = '';

  @override
  void initState() {
    super.initState();
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    services.sharedAccess
        .listPending(primaryAddress)
        .then((a) => setState(() => _actions = a))
        .catchError((err) => setState(() => _error = err.toString()));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Approvals')),
      body: _error.isNotEmpty
          ? Padding(padding: const EdgeInsets.all(16), child: Text(_error, style: const TextStyle(color: Colors.red)))
          : _actions == null
              ? const Padding(padding: EdgeInsets.all(16), child: Text('Loading…'))
              : _actions!.isEmpty
                  ? const Padding(padding: EdgeInsets.all(16), child: Text('No pending actions.'))
                  : ListView(
                      padding: const EdgeInsets.all(16),
                      children: _actions!
                          .map(
                            (a) => Card(
                              child: ListTile(
                                title: Text(a.kind.replaceAll('_', ' ')),
                                subtitle: Text('${a.description.isNotEmpty ? a.description : 'Group #${a.groupId}'}\n${a.status}'),
                                isThreeLine: true,
                                trailing: TextButton(
                                  onPressed: () => Navigator.of(context).pushNamed('/shared-access/approvals/${a.id}'),
                                  child: const Text('Review'),
                                ),
                              ),
                            ),
                          )
                          .toList(),
                    ),
    );
  }
}
