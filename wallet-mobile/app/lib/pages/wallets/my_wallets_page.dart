// Mirrors wallet-web's pages/wallets/MyWallets.tsx - the original's own
// wallet directory. A wallet's Tag/Description/Alias exist so it can be
// shown and referred to in a friendly way (Tag/Alias) rather than by its
// raw address, and so a payment can be sent to it by alias alongside
// address/username/email (see payments_api.dart/send_page.dart and
// wallet-backend users.UserWallet's own doc comment). This page lists the
// caller's own directory and lets them edit each entry's Tag/Description/
// Alias in place.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../api/users_api.dart';

class MyWalletsPage extends StatefulWidget {
  const MyWalletsPage({super.key});

  @override
  State<MyWalletsPage> createState() => _MyWalletsPageState();
}

class _MyWalletsPageState extends State<MyWalletsPage> {
  List<UserWallet> _wallets = [];
  bool _loading = true;
  String _error = '';

  @override
  void initState() {
    super.initState();
    _reload();
  }

  void _reload() {
    final services = context.read<AppServices>();
    final signerAddress = context.read<WalletState>().signer.address;
    if (signerAddress == null) return;
    setState(() => _loading = true);
    services.users
        .listMyWallets(signerAddress)
        .then((w) => setState(() {
              _wallets = w;
              _loading = false;
            }))
        .catchError((err) => setState(() {
              _error = err.toString();
              _loading = false;
            }));
  }

  @override
  Widget build(BuildContext context) {
    final signerAddress = context.watch<WalletState>().signer.address;
    return Scaffold(
      appBar: AppBar(title: const Text('My Wallets')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const Text(
            'Give each of your wallets a tag, description, and alias - the alias can be used to send a payment to '
            'this wallet, the same way an address, username, or email can.',
            style: TextStyle(color: Colors.grey),
          ),
          const SizedBox(height: 16),
          if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
          if (_loading) const Text('Loading…'),
          for (final w in _wallets)
            Padding(
              padding: const EdgeInsets.only(bottom: 16),
              child: _WalletCard(wallet: w, signerAddress: signerAddress, onSaved: _reload),
            ),
        ],
      ),
    );
  }
}

class _WalletCard extends StatefulWidget {
  const _WalletCard({required this.wallet, required this.signerAddress, required this.onSaved});
  final UserWallet wallet;
  final String? signerAddress;
  final VoidCallback onSaved;

  @override
  State<_WalletCard> createState() => _WalletCardState();
}

class _WalletCardState extends State<_WalletCard> {
  late String _tag = widget.wallet.tag;
  late String _description = widget.wallet.description;
  late String _alias = widget.wallet.alias;
  bool _busy = false;
  String _error = '';
  bool _saved = false;

  bool get _dirty => _tag != widget.wallet.tag || _description != widget.wallet.description || _alias != widget.wallet.alias;

  Future<void> _handleSave() async {
    final services = context.read<AppServices>();
    final signerAddress = widget.signerAddress;
    if (signerAddress == null) return;
    setState(() {
      _busy = true;
      _error = '';
      _saved = false;
    });
    try {
      await services.users.updateWalletMetadata(signerAddress, widget.wallet.address, tag: _tag, description: _description, alias: _alias);
      setState(() => _saved = true);
      widget.onSaved();
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(color: const Color(0xFFF2F6F9), borderRadius: BorderRadius.circular(12)),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Expanded(child: Text(widget.wallet.address, style: const TextStyle(fontSize: 11, color: Colors.grey))),
              if (widget.wallet.isPrimary) const Text('Primary', style: TextStyle(fontSize: 11, fontWeight: FontWeight.bold)),
            ],
          ),
          const SizedBox(height: 8),
          AppTextInput(label: 'Tag', value: _tag, hint: 'e.g. savings', onChanged: (v) => setState(() => _tag = v)),
          const SizedBox(height: 8),
          AppTextInput(label: 'Description', value: _description, hint: 'What this wallet is for', onChanged: (v) => setState(() => _description = v)),
          const SizedBox(height: 8),
          AppTextInput(label: 'Alias', value: _alias, hint: 'A friendly name others can send to', onChanged: (v) => setState(() => _alias = v)),
          if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
          if (_saved && !_dirty) const Padding(padding: EdgeInsets.only(top: 8), child: Text('Saved.', style: TextStyle(color: Colors.green))),
          const SizedBox(height: 8),
          AppButton(label: _busy ? 'Saving…' : 'Save', onPressed: _busy || !_dirty || _alias.isEmpty ? null : _handleSave),
        ],
      ),
    );
  }
}
