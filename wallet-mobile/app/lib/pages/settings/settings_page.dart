// Mirrors wallet-web's pages/settings/Settings.tsx (partial - the network
// switch UI and danger-zone wipe confirmation dialog are tracked as
// follow-ups, not ported in this pass; see wallet-mobile/PLAN.md). The
// Recovery section (RecoverySettings.tsx) is its own page here, linked
// below, rather than inlined.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button_secondary.dart';
import 'recovery_settings_page.dart';

class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key});

  @override
  Widget build(BuildContext context) {
    final wallet = context.watch<WalletState>();
    final services = context.read<AppServices>();
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text('Wallets', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Text('Signer: ${wallet.signer.address ?? 'not set'}'),
          Text('Primary wallet: ${wallet.primary.address ?? 'not set'}'),
          const SizedBox(height: 24),
          Text('Network', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Text('Currently connected to ${services.network.getNetworkConfig().label}.'),
          const SizedBox(height: 24),
          Text('Recovery', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          AppButtonSecondary(
            label: 'Manage recovery methods',
            onPressed: () => Navigator.of(context).push(MaterialPageRoute(builder: (_) => const RecoverySettingsPage())),
          ),
          const SizedBox(height: 24),
          Text('Danger zone', style: TextStyle(color: Colors.red.shade700, fontWeight: FontWeight.bold)),
          const SizedBox(height: 8),
          AppButtonSecondary(
            label: 'Remove wallets from this device',
            onPressed: () async {
              await services.walletCore.wipeWallet('signer');
              wallet.walletRemoved(role: 'signer');
              wallet.walletRemoved(role: 'primary');
              wallet.signedOut();
              if (context.mounted) Navigator.of(context).pushNamedAndRemoveUntil('/onboarding', (route) => false);
            },
          ),
        ],
      ),
    );
  }
}
