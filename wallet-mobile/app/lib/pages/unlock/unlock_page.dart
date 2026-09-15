// Mirrors wallet-web's pages/unlock/Unlock.tsx. Unlocking works fully
// offline - this page makes no network calls at all, only wallet-core
// calls (local Argon2id decrypt). Only 'signer' ever appears in
// rolesToUnlock in practice - the primary wallet is a Safe with nothing
// to unlock (see WalletState's own doc comment).
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../widgets/auth_layout.dart';

class UnlockPage extends StatefulWidget {
  const UnlockPage({super.key});

  @override
  State<UnlockPage> createState() => _UnlockPageState();
}

class _UnlockPageState extends State<UnlockPage> {
  String _password = '';
  String _error = '';
  bool _busy = false;

  Future<void> _handleUnlock() async {
    setState(() {
      _error = '';
      _busy = true;
    });
    try {
      final services = context.read<AppServices>();
      final wallet = context.read<WalletState>();
      final address = await services.walletCore.unlockWallet('signer', _password);
      wallet.unlocked(role: 'signer', address: address);
      if (mounted) Navigator.of(context).pushReplacementNamed('/dashboard');
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final wallet = context.watch<WalletState>();
    final needsUnlock = wallet.signer.hasVault && !wallet.signer.isUnlocked;

    return AuthLayout(
      title: 'Welcome back',
      subtitle: 'Unlock your wallet to continue.',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (needsUnlock) AppTextInput(label: 'Signer password', value: _password, obscureText: true, onChanged: (v) => _password = v),
          if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
          const SizedBox(height: 12),
          AppButton(label: 'Unlock', onPressed: _busy || !needsUnlock ? null : _handleUnlock),
          // TODO(wallet-mobile): a "lost your key?" recovery entry point,
          // mirroring wallet-web's RecoveryWizard (wallet-web PLAN.md
          // §16) - not yet ported here, see wallet-mobile/PLAN.md's
          // updated roadmap for status.
        ],
      ),
    );
  }
}
