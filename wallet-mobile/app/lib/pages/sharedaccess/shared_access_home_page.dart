// Mirrors wallet-web's pages/sharedaccess/SharedAccessHome.tsx - the
// original's dashboard/sharedAccess/{landing,add} pages, folded into one
// tabbed page. Per-group detail/actions live in group_detail_page.dart;
// pending approvals live in approvals_page.dart.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../api/sharedaccess_api.dart';

class SharedAccessHomePage extends StatelessWidget {
  const SharedAccessHomePage({super.key});

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('Shared access'),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pushNamed('/shared-access/approvals'),
              child: const Text('Approvals', style: TextStyle(color: Colors.white)),
            ),
          ],
          bottom: const TabBar(tabs: [Tab(text: 'My wallets'), Tab(text: 'Create group')]),
        ),
        body: const TabBarView(children: [_MyWallets(), _NewGroupForm()]),
      ),
    );
  }
}

enum _WalletFilter { mine, sharedWithMe }

class _MyWallets extends StatefulWidget {
  const _MyWallets();

  @override
  State<_MyWallets> createState() => _MyWalletsState();
}

class _MyWalletsState extends State<_MyWallets> {
  List<WalletSummary>? _wallets;
  String _error = '';
  _WalletFilter _filter = _WalletFilter.mine;

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
        .listWallets(primaryAddress)
        .then((w) => setState(() => _wallets = w))
        .catchError((err) => setState(() => _error = err.toString()));
  }

  @override
  Widget build(BuildContext context) {
    if (_error.isNotEmpty) return Padding(padding: const EdgeInsets.all(16), child: Text(_error, style: const TextStyle(color: Colors.red)));
    if (_wallets == null) return const Padding(padding: EdgeInsets.all(16), child: Text('Loading…'));

    final filtered = _wallets!.where((w) => _filter == _WalletFilter.mine ? w.isOwner : !w.isOwner).toList();

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Row(
          children: [
            ChoiceChip(
              label: const Text('My Wallets'),
              selected: _filter == _WalletFilter.mine,
              onSelected: (_) => setState(() => _filter = _WalletFilter.mine),
            ),
            const SizedBox(width: 8),
            ChoiceChip(
              label: const Text('Wallets Shared With Me'),
              selected: _filter == _WalletFilter.sharedWithMe,
              onSelected: (_) => setState(() => _filter = _WalletFilter.sharedWithMe),
            ),
          ],
        ),
        const SizedBox(height: 12),
        if (filtered.isEmpty)
          Text(_filter == _WalletFilter.mine ? 'No wallets found.' : 'No wallets have been shared with you.'),
        ...filtered.map(
          (w) => Card(
            child: ListTile(
              title: Row(
                children: [
                  Flexible(child: Text(w.name ?? (w.kind == 'primary' ? 'Primary wallet' : 'Group #${w.groupId}'))),
                  if (w.isOwner && w.isShared) const Padding(padding: EdgeInsets.only(left: 6), child: Icon(Icons.group, size: 16)),
                ],
              ),
              subtitle: Text(
                '${w.address}\n${w.role}${w.kind == 'group' ? ' · threshold ${w.threshold}' : ''}${w.disabled ? ' · disabled' : ''}',
              ),
              isThreeLine: true,
              trailing: w.kind == 'group' && w.groupId != null
                  ? TextButton(
                      onPressed: () => Navigator.of(context).pushNamed('/shared-access/groups/${w.groupId}'),
                      child: const Text('Manage'),
                    )
                  : null,
            ),
          ),
        ),
      ],
    );
  }
}

class _NewGroupForm extends StatefulWidget {
  const _NewGroupForm();

  @override
  State<_NewGroupForm> createState() => _NewGroupFormState();
}

class _MemberRow {
  _MemberRow({this.username = ''});
  String username;
  GroupRole role = GroupRole.approver;
}

class _NewGroupFormState extends State<_NewGroupForm> {
  String _name = '';
  String _threshold = '1';
  late final List<_MemberRow> _members;
  bool _busy = false;
  String _error = '';
  int? _created;

  @override
  void initState() {
    super.initState();
    // The creator isn't implicitly added as a member server-side - pre-fill
    // the first row with the caller's own username so an easy-to-miss step
    // doesn't lock the creator out of their own new group.
    final myUsername = context.read<WalletState>().username;
    _members = [_MemberRow(username: myUsername ?? '')];
  }

  Future<void> _submit() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() {
      _error = '';
      _busy = true;
    });
    try {
      final group = await services.sharedAccess.createGroup(
        primaryAddress,
        _name,
        int.tryParse(_threshold) ?? 1,
        _members.where((m) => m.username.isNotEmpty).map((m) => (username: m.username, role: m.role)).toList(),
      );
      setState(() => _created = group.id);
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        const Text(
          "Create a group wallet controlled by a threshold of its members' approvals - no single member can move funds alone.",
        ),
        const SizedBox(height: 12),
        AppTextInput(label: 'Group name', value: _name, hint: 'Family wallet', onChanged: (v) => _name = v),
        const SizedBox(height: 12),
        AppTextInput(label: 'Approval threshold', value: _threshold, onChanged: (v) => _threshold = v),
        const SizedBox(height: 12),
        const Text('Members'),
        const SizedBox(height: 4),
        ..._members.map((m) {
          return Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: TextEditingController(text: m.username),
                    decoration: const InputDecoration(hintText: 'username'),
                    onChanged: (v) => m.username = v,
                  ),
                ),
                const SizedBox(width: 8),
                DropdownButton<GroupRole>(
                  value: m.role,
                  items: GroupRole.values.map((r) => DropdownMenuItem(value: r, child: Text(r.label))).toList(),
                  onChanged: (r) => setState(() => m.role = r ?? m.role),
                ),
              ],
            ),
          );
        }),
        TextButton(
          onPressed: () => setState(() => _members.add(_MemberRow())),
          child: const Text('+ Add another member'),
        ),
        if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
        if (_created != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: TextButton(
              onPressed: () => Navigator.of(context).pushNamed('/shared-access/groups/$_created'),
              child: const Text('Group created. Open it'),
            ),
          ),
        const SizedBox(height: 12),
        AppButton(label: _busy ? 'Creating…' : 'Create group', onPressed: _busy || _name.isEmpty ? null : _submit),
      ],
    );
  }
}
