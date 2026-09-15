// Mirrors wallet-web's pages/settings/RecoverySettings.tsx.
// wallet-backend PLAN.md §15: two independent recovery branches. This
// screen lets the signed-in owner set up the shared security-question
// factor and toggle each branch on/off - the actual recovery *execution*
// (for someone who's already locked out) lives at recovery_wizard_page
// instead, reachable without being signed in at all.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_button_secondary.dart';
import '../../widgets/app_text_input.dart';
import '../../api/users_api.dart';
import '../../api/recovery_api.dart';

class RecoverySettingsPage extends StatefulWidget {
  const RecoverySettingsPage({super.key});

  @override
  State<RecoverySettingsPage> createState() => _RecoverySettingsPageState();
}

class _RecoverySettingsPageState extends State<RecoverySettingsPage> {
  User? _user;
  List<SecurityQuestion> _questions = [];
  final Map<int, String> _answers = {};
  bool _busy = false;
  String _error = '';
  String _status = '';

  @override
  void initState() {
    super.initState();
    _refresh();
    context.read<AppServices>().recovery.listSecurityQuestions().then((q) => setState(() => _questions = q)).catchError((_) {
      setState(() => _questions = []);
    });
  }

  void _refresh() {
    final services = context.read<AppServices>();
    final signerAddress = context.read<WalletState>().signer.address;
    if (signerAddress == null) return;
    services.users.getMyUser(signerAddress).then((u) => setState(() => _user = u)).catchError((_) => setState(() => _user = null));
  }

  Future<void> _runAction(Future<void> Function() fn) async {
    setState(() {
      _error = '';
      _status = '';
      _busy = true;
    });
    try {
      await fn();
      _refresh();
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  Future<void> _handleSaveAnswers() => _runAction(() async {
        final services = context.read<AppServices>();
        final primaryAddress = context.read<WalletState>().primary.address;
        if (primaryAddress == null) throw Exception('Primary wallet address is not known yet.');
        final entries = _answers.entries.where((e) => e.value.trim().isNotEmpty).toList();
        if (entries.isEmpty) throw Exception('Answer at least one security question first.');
        for (final e in entries) {
          await services.recovery.setSecurityAnswer(primaryAddress, e.key, e.value);
        }
        setState(() {
          _answers.clear();
          _status = 'Security answers saved.';
        });
      });

  Future<void> _handleToggleAccountRecovery(bool enable) => _runAction(() async {
        final services = context.read<AppServices>();
        final primaryAddress = context.read<WalletState>().primary.address;
        if (primaryAddress == null) throw Exception('Primary wallet address is not known yet.');
        if (enable) {
          await services.recovery.enableAccountRecovery(primaryAddress);
        } else {
          await services.recovery.disableAccountRecovery(primaryAddress);
        }
        setState(() => _status = enable ? 'Account recovery enabled.' : 'Account recovery disabled.');
      });

  Future<void> _handleEnableWalletRecovery() => _runAction(() async {
        final services = context.read<AppServices>();
        final wallet = context.read<WalletState>();
        final primaryAddress = wallet.primary.address;
        if (primaryAddress == null || wallet.signer.address == null) throw Exception('Wallet is not fully set up yet.');
        final challenge = await services.recovery.buildEnableWalletRecovery(primaryAddress);
        if (challenge.feeTx != null) {
          throw Exception(
            'This deployment charges a one-time fee to enable wallet recovery, and broadcasting that fee '
            'transaction yourself is not yet supported in this app. Contact support to enable it.',
          );
        }
        final addOwnerSignature = await services.walletCore.signHexDigest('signer', challenge.addOwnerSafeTxHash);
        final setGuardSignature = await services.walletCore.signHexDigest('signer', challenge.setGuardSafeTxHash);
        await services.recovery.confirmEnableWalletRecovery(primaryAddress, '', addOwnerSignature, setGuardSignature);
        setState(() => _status = 'Wallet recovery enabled.');
      });

  Future<void> _handleDisableWalletRecovery() => _runAction(() async {
        final services = context.read<AppServices>();
        final primaryAddress = context.read<WalletState>().primary.address;
        if (primaryAddress == null) throw Exception('Primary wallet address is not known yet.');
        final challenge = await services.recovery.buildDisableWalletRecovery(primaryAddress);
        final removeOwnerSignature = await services.walletCore.signHexDigest('signer', challenge.removeOwnerSafeTxHash);
        final clearGuardSignature = await services.walletCore.signHexDigest('signer', challenge.clearGuardSafeTxHash);
        await services.recovery.confirmDisableWalletRecovery(primaryAddress, removeOwnerSignature, clearGuardSignature);
        setState(() => _status = 'Wallet recovery disabled.');
      });

  @override
  Widget build(BuildContext context) {
    final user = _user;
    return Scaffold(
      appBar: AppBar(title: const Text('Recovery')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
          if (_status.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 8), child: Text(_status, style: const TextStyle(color: Colors.green))),

          Text('Security questions', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 4),
          const Text(
            "Both recovery methods below need at least one answered security question, plus an emailed one-time "
            "code, to prove it's really you.",
            style: TextStyle(color: Colors.grey),
          ),
          const SizedBox(height: 8),
          for (final q in _questions)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: AppTextInput(label: q.question, value: _answers[q.id] ?? '', onChanged: (v) => _answers[q.id] = v),
            ),
          AppButtonSecondary(label: 'Save answers', onPressed: _busy ? null : _handleSaveAnswers),

          const SizedBox(height: 24),
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(border: Border.all(color: Colors.grey.shade300), borderRadius: BorderRadius.circular(8)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Account recovery ${user?.accountRecoveryEnabled == true ? '(enabled)' : '(disabled)'}', style: const TextStyle(fontWeight: FontWeight.w600)),
                const SizedBox(height: 4),
                const Text(
                  'Free. If you lose your key, your account gets a fresh wallet address - anything at the old address is abandoned.',
                  style: TextStyle(color: Colors.grey, fontSize: 13),
                ),
                const SizedBox(height: 8),
                if (user?.accountRecoveryEnabled == true)
                  AppButtonSecondary(label: 'Disable', onPressed: _busy ? null : () => _handleToggleAccountRecovery(false))
                else
                  AppButton(label: 'Enable', onPressed: _busy ? null : () => _handleToggleAccountRecovery(true)),
              ],
            ),
          ),

          const SizedBox(height: 16),
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(border: Border.all(color: Colors.grey.shade300), borderRadius: BorderRadius.circular(8)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('Wallet recovery ${user?.walletRecoveryEnabled == true ? '(enabled)' : '(disabled)'}', style: const TextStyle(fontWeight: FontWeight.w600)),
                const SizedBox(height: 4),
                const Text(
                  'Keeps your existing wallet address, sub-wallets, and shared-access memberships if you lose your '
                  'key - a recovery service is added as a second owner of your wallet, restricted to swapping owners only.',
                  style: TextStyle(color: Colors.grey, fontSize: 13),
                ),
                const SizedBox(height: 8),
                if (user?.primaryWalletDeployed != true)
                  const Text('Your primary wallet must be deployed on-chain first.', style: TextStyle(color: Colors.grey, fontSize: 12))
                else if (user?.walletRecoveryEnabled == true)
                  AppButtonSecondary(label: 'Disable', onPressed: _busy ? null : _handleDisableWalletRecovery)
                else
                  AppButton(label: 'Enable', onPressed: _busy ? null : _handleEnableWalletRecovery),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
