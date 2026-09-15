// Mirrors wallet-web's pages/dashboard/Dashboard.tsx (simplified): shows
// curated-asset balances and payment history, falling back to a plain
// "couldn't load" message rather than the offline-cache-with-timestamp
// treatment wallet-web's own dashboard gives this (OfflineCache exists
// here too - see store/offline_cache.dart - but wiring it into this
// specific screen is tracked as a follow-up, not done in this pass).
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../api/assets_api.dart';
import '../../api/payments_api.dart';

class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key});

  @override
  State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  List<CuratedToken> _tokens = [];
  List<PaymentHistoryRecord> _history = [];
  String? _nativeBalance;
  bool _loading = true;
  String _error = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final services = context.read<AppServices>();
    final wallet = context.read<WalletState>();
    final address = wallet.primary.address;
    try {
      final tokens = await services.assets.listCuratedTokens();
      String? balance;
      List<PaymentHistoryRecord> history = [];
      if (address != null) {
        balance = await services.assets.getBalance(address);
        history = await services.payments.getPaymentHistory(address);
      }
      if (!mounted) return;
      setState(() {
        _tokens = tokens;
        _nativeBalance = balance;
        _history = history;
        _loading = false;
      });
    } catch (err) {
      if (!mounted) return;
      setState(() {
        _error = err.toString();
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final wallet = context.watch<WalletState>();
    return Scaffold(
      appBar: AppBar(title: const Text('Dashboard')),
      drawer: _AppDrawer(),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            Text('Wallet', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 4),
            Text(wallet.primary.address ?? 'not set', style: const TextStyle(fontFamily: 'monospace', fontSize: 12)),
            const SizedBox(height: 24),
            Text('Balances', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            if (_loading) const CircularProgressIndicator()
            else if (_error.isNotEmpty) Text(_error, style: const TextStyle(color: Colors.red))
            else ...[
              ListTile(title: const Text('ETH (native)'), trailing: Text(_nativeBalance ?? '0')),
              for (final token in _tokens) ListTile(title: Text(token.symbol), subtitle: Text(token.name)),
            ],
            const SizedBox(height: 24),
            Text('Payment history', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            if (!_loading && _history.isEmpty) const Text('No payments yet.'),
            for (final record in _history)
              ListTile(
                title: Text('${record.amount} to ${record.toAddress}'),
                subtitle: Text(record.txHash, style: const TextStyle(fontFamily: 'monospace', fontSize: 11)),
              ),
          ],
        ),
      ),
    );
  }
}

class _AppDrawer extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Drawer(
      child: ListView(
        children: [
          const DrawerHeader(child: Text('Trovo Wallet')),
          ListTile(leading: const Icon(Icons.dashboard), title: const Text('Dashboard'), onTap: () => Navigator.of(context).pop()),
          ListTile(
            leading: const Icon(Icons.send),
            title: const Text('Send'),
            onTap: () {
              Navigator.of(context).pop();
              Navigator.of(context).pushNamed('/send');
            },
          ),
          ListTile(
            leading: const Icon(Icons.settings),
            title: const Text('Settings'),
            onTap: () {
              Navigator.of(context).pop();
              Navigator.of(context).pushNamed('/settings');
            },
          ),
        ],
      ),
    );
  }
}
