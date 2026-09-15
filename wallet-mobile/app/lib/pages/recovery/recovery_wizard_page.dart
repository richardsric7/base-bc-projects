// Mirrors wallet-web's pages/recovery/RecoveryWizard.tsx.
// PLAN.md §13/wallet-backend PLAN.md §15: recovery is reachable without
// being logged in by definition (the whole point is having no working
// signer key) - a brand-new signer vault is generated right here, before
// any successful login, the one place in this app that happens.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../store/offline_cache.dart';
import '../../widgets/auth_layout.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_button_secondary.dart';
import '../../widgets/app_text_input.dart';
import '../../widgets/mnemonic_reveal.dart';
import '../../api/recovery_api.dart';

enum _Branch { account, wallet }

enum _Step { username, chooseBranch, newSignerReveal, newSignerPassword, factors, done }

class RecoveryWizardPage extends StatefulWidget {
  const RecoveryWizardPage({super.key});

  @override
  State<RecoveryWizardPage> createState() => _RecoveryWizardPageState();
}

class _RecoveryWizardPageState extends State<RecoveryWizardPage> {
  _Step _step = _Step.username;
  String _error = '';
  bool _busy = false;

  String _username = '';
  bool _accountRecoveryAvailable = false;
  bool _walletRecoveryAvailable = false;
  _Branch _branch = _Branch.account;

  String _newPhrase = '';
  String _newAddress = '';
  String _password = '';
  String _confirmPassword = '';

  String _otp = '';
  bool _otpRequested = false;
  List<SecurityQuestion> _questions = [];
  final Map<int, String> _answers = {};

  String _resultAddress = '';
  String _resultTxHash = '';

  Future<void> _runStep(Future<void> Function() fn) async {
    setState(() {
      _error = '';
      _busy = true;
    });
    try {
      await fn();
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  Future<void> _loadQuestionsIfNeeded() async {
    if (_questions.isNotEmpty) return;
    try {
      final questions = await context.read<AppServices>().recovery.listSecurityQuestions();
      setState(() => _questions = questions);
    } catch (_) {
      setState(() => _questions = []);
    }
  }

  Future<void> _handleUsernameSubmit() => _runStep(() async {
        if (_username.isEmpty) throw Exception('Enter your username.');
        final services = context.read<AppServices>();
        final user = await services.users.getUserByUsername(_username);
        if (!user.accountRecoveryEnabled && !user.walletRecoveryEnabled) {
          throw Exception('This account has no recovery method enabled. Contact support for help.');
        }
        _accountRecoveryAvailable = user.accountRecoveryEnabled;
        _walletRecoveryAvailable = user.walletRecoveryEnabled;
        _branch = (user.walletRecoveryEnabled && !user.accountRecoveryEnabled) ? _Branch.wallet : _Branch.account;
        setState(() {
          _step = (user.accountRecoveryEnabled && user.walletRecoveryEnabled) ? _Step.chooseBranch : _Step.newSignerReveal;
        });
      });

  Future<void> _handleGenerateNewSigner() => _runStep(() async {
        final services = context.read<AppServices>();
        final mnemonic = await services.walletCore.generateMnemonic();
        setState(() {
          _newPhrase = mnemonic;
          _step = _Step.newSignerReveal;
        });
      });

  Future<void> _handleNewSignerPasswordSubmit() => _runStep(() async {
        if (_password.length < 8) throw Exception('Password must be at least 8 characters.');
        if (_password != _confirmPassword) throw Exception('Passwords do not match.');
        final services = context.read<AppServices>();
        final address = await services.walletCore.createVault('signer', _newPhrase, _password);
        setState(() {
          _newPhrase = '';
          _newAddress = address;
          _step = _Step.factors;
        });
        await _loadQuestionsIfNeeded();
      });

  Future<void> _handleRequestOtp() => _runStep(() async {
        await context.read<AppServices>().recovery.requestAccountRecoveryOTP(_username);
        setState(() => _otpRequested = true);
      });

  Future<void> _handleFinish() => _runStep(() async {
        if (_otp.isEmpty) throw Exception('Enter the OTP sent to your registered email.');
        final answerInputs = _answers.entries
            .where((e) => e.value.trim().isNotEmpty)
            .map((e) => SecurityAnswerInput(securityQuestionId: e.key, answer: e.value))
            .toList();
        if (answerInputs.isEmpty) throw Exception('Answer at least one of your security questions.');

        final services = context.read<AppServices>();
        final wallet = context.read<WalletState>();
        // A human-composed string message, never signHexDigest - see
        // recovery_api.dart's own doc comments for why that distinction
        // matters (wallet-web/PLAN.md §15).
        final message = buildRecoveryMessage(_username, _newAddress);
        final signature = await services.walletCore.signRequestMessage('signer', message);

        if (_branch == _Branch.account) {
          final log = await services.recovery.recoverAccount(_username, _newAddress, signature, _otp, answerInputs);
          wallet.vaultCreated(role: 'signer', address: _newAddress);
          // Branch A's own design (wallet-backend PLAN.md §15.8): the
          // recovered account is a bare, undeployed EOA-as-wallet
          // identity - signer == wallet == newAddress, not a Safe.
          wallet.vaultCreated(role: 'primary', address: _newAddress);
          wallet.profileRegistered(_username);
          await services.cache.setEntry(primaryWalletAddressKey(_newAddress), _newAddress);
          setState(() => _resultAddress = log.newAddress);
        } else {
          final log = await services.recovery.recoverWallet(_username, _newAddress, signature, _otp, answerInputs);
          wallet.vaultCreated(role: 'signer', address: _newAddress);
          wallet.vaultCreated(role: 'primary', address: log.walletAddress);
          wallet.profileRegistered(_username);
          await services.cache.setEntry(primaryWalletAddressKey(_newAddress), log.walletAddress);
          setState(() {
            _resultAddress = log.walletAddress;
            _resultTxHash = log.txHash;
          });
        }
        setState(() => _step = _Step.done);
      });

  @override
  Widget build(BuildContext context) {
    return AuthLayout(
      title: 'Account recovery',
      subtitle: 'Regain access using your security answers and email OTP.',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
          if (_step == _Step.username) ..._usernameStep(),
          if (_step == _Step.chooseBranch) ..._chooseBranchStep(),
          if (_step == _Step.newSignerReveal && _newPhrase.isEmpty) ..._generateSignerStep(),
          if (_step == _Step.newSignerReveal && _newPhrase.isNotEmpty)
            MnemonicReveal(mnemonic: _newPhrase, onConfirmed: () => setState(() => _step = _Step.newSignerPassword)),
          if (_step == _Step.newSignerPassword) ..._passwordStep(),
          if (_step == _Step.factors) ..._factorsStep(),
          if (_step == _Step.done) ..._doneStep(),
        ],
      ),
    );
  }

  List<Widget> _usernameStep() => [
        Text("What's your username?", style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 12),
        AppTextInput(label: 'Username', value: _username, onChanged: (v) => _username = v),
        const SizedBox(height: 12),
        AppButton(label: 'Continue', onPressed: _busy || _username.isEmpty ? null : _handleUsernameSubmit),
        const SizedBox(height: 8),
        AppButtonSecondary(label: 'Back to unlock', onPressed: () => Navigator.of(context).pushReplacementNamed('/unlock')),
      ];

  List<Widget> _chooseBranchStep() => [
        Text('Choose a recovery method', style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        const Text(
          'Wallet recovery keeps your existing wallet address, sub-wallets, and shared-access memberships. '
          'Account recovery is faster but starts you over with a fresh, empty wallet.',
          style: TextStyle(color: Colors.grey),
        ),
        const SizedBox(height: 12),
        if (_walletRecoveryAvailable)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: AppButtonSecondary(
              label: 'Wallet recovery (keep everything)',
              onPressed: () => setState(() {
                _branch = _Branch.wallet;
                _step = _Step.newSignerReveal;
              }),
            ),
          ),
        if (_accountRecoveryAvailable)
          AppButtonSecondary(
            label: 'Account recovery (fresh start)',
            onPressed: () => setState(() {
              _branch = _Branch.account;
              _step = _Step.newSignerReveal;
            }),
          ),
      ];

  List<Widget> _generateSignerStep() => [
        Text('Generate a new signer key', style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        const Text(
          "This replaces the key you lost access to. Write down the new recovery phrase - you'll need it every "
          'time you use this wallet from now on.',
          style: TextStyle(color: Colors.grey),
        ),
        const SizedBox(height: 12),
        AppButton(label: 'Generate new key', onPressed: _busy ? null : _handleGenerateNewSigner),
      ];

  List<Widget> _passwordStep() => [
        Text('Set a password', style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        const Text('This encrypts your new signer wallet on this device.', style: TextStyle(color: Colors.grey)),
        const SizedBox(height: 12),
        AppTextInput(label: 'Password', value: _password, obscureText: true, onChanged: (v) => _password = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Confirm password', value: _confirmPassword, obscureText: true, onChanged: (v) => _confirmPassword = v),
        const SizedBox(height: 12),
        AppButton(label: 'Continue', onPressed: _busy ? null : _handleNewSignerPasswordSubmit),
      ];

  List<Widget> _factorsStep() => [
        Text("Verify it's you", style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 12),
        const Text("We'll email a one-time code to your registered address.", style: TextStyle(color: Colors.grey)),
        const SizedBox(height: 8),
        AppButtonSecondary(label: _otpRequested ? 'Resend code' : 'Send code', onPressed: _busy ? null : _handleRequestOtp),
        const SizedBox(height: 8),
        AppTextInput(label: 'Code from email', value: _otp, onChanged: (v) => setState(() => _otp = v)),
        const SizedBox(height: 16),
        const Text('Answer at least one security question you set up previously.', style: TextStyle(color: Colors.grey)),
        const SizedBox(height: 8),
        for (final q in _questions)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: AppTextInput(label: q.question, value: _answers[q.id] ?? '', onChanged: (v) => _answers[q.id] = v),
          ),
        const SizedBox(height: 8),
        AppButton(label: 'Recover account', onPressed: _busy || _otp.isEmpty ? null : _handleFinish),
      ];

  List<Widget> _doneStep() => [
        Text('Recovery complete', style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        Text('Your wallet address: $_resultAddress', style: const TextStyle(color: Colors.grey)),
        if (_resultTxHash.isNotEmpty)
          Padding(padding: const EdgeInsets.only(top: 4), child: Text('Recovery transaction: $_resultTxHash', style: const TextStyle(color: Colors.grey))),
        const SizedBox(height: 12),
        AppButton(label: 'Go to dashboard', onPressed: () => Navigator.of(context).pushNamedAndRemoveUntil('/dashboard', (route) => false)),
      ];
}
