// Mirrors wallet-web's pages/onboarding/OnboardingWizard.tsx exactly -
// same steps, same reasoning (no separate "primary wallet" mnemonic; the
// primary wallet is a Safe with no private key of its own, wallet-backend
// PLAN.md §13/§17 - see finishWithPrimaryWallet below).
import 'dart:async';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../store/offline_cache.dart';
import '../../api/http_client.dart';
import '../../widgets/auth_layout.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_button_secondary.dart';
import '../../widgets/app_text_input.dart';
import '../../widgets/mnemonic_reveal.dart';

enum _Step { chooseSigner, createSignerReveal, importSigner, signerPassword, account, done }

class OnboardingWizard extends StatefulWidget {
  const OnboardingWizard({super.key});

  @override
  State<OnboardingWizard> createState() => _OnboardingWizardState();
}

class _OnboardingWizardState extends State<OnboardingWizard> {
  _Step _step = _Step.chooseSigner;
  String _error = '';
  bool _busy = false;

  String _signerPhrase = '';
  String _password = '';
  String _confirmPassword = '';
  String _signerAddress = '';
  String _username = '';
  String _email = '';

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
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _handleGenerateNew() => _runStep(() async {
        final services = context.read<AppServices>();
        final mnemonic = await services.walletCore.generateMnemonic(wordCount: 12);
        setState(() {
          _signerPhrase = mnemonic;
          _step = _Step.createSignerReveal;
        });
      });

  void _handleImportExisting() => setState(() => _step = _Step.importSigner);

  Future<void> _handleImportSignerSubmit() => _runStep(() async {
        final services = context.read<AppServices>();
        final valid = await services.walletCore.validateMnemonic(_signerPhrase);
        if (!valid) {
          throw Exception('That does not look like a valid recovery phrase. Check the words and try again.');
        }
        setState(() => _step = _Step.signerPassword);
      });

  /// Records the wallet-backend-computed primary wallet address (a Safe
  /// smart-contract account) as this app's "primary" wallet - there is
  /// no local mnemonic/vault for it to create.
  Future<void> _finishWithPrimaryWallet(String signerAddr, String primaryWalletAddress) async {
    final wallet = context.read<WalletState>();
    final services = context.read<AppServices>();
    wallet.vaultCreated(role: 'primary', address: primaryWalletAddress);
    await services.cache.setEntry(primaryWalletAddressKey(signerAddr), primaryWalletAddress);
    // Best-effort and non-blocking, matching wallet-web's own reasoning:
    // an undeployed Safe has no shared-access group and can't build/
    // submit a payment yet - Send's own withWalletDeployRetry is the
    // real backstop.
    unawaited(services.users.deployPrimaryWallet(signerAddr));
    setState(() => _step = _Step.done);
  }

  Future<void> _handleSignerPasswordSubmit() => _runStep(() async {
        if (_password.length < 8) throw Exception('Password must be at least 8 characters.');
        if (_password != _confirmPassword) throw Exception('Passwords do not match.');
        final services = context.read<AppServices>();
        final wallet = context.read<WalletState>();
        final address = await services.walletCore.createVault('signer', _signerPhrase, _password);
        setState(() {
          _signerPhrase = '';
          _signerAddress = address;
        });
        wallet.vaultCreated(role: 'signer', address: address);

        try {
          final user = await services.users.getMyUser(address);
          wallet.profileRegistered(user.username);
          await _finishWithPrimaryWallet(address, user.address);
        } on ApiError {
          setState(() => _step = _Step.account);
        }
      });

  Future<void> _handleAccountSubmit() => _runStep(() async {
        if (_username.isEmpty || _email.isEmpty) throw Exception('Username and email are required.');
        final services = context.read<AppServices>();
        final wallet = context.read<WalletState>();
        final user = await services.users.registerUser(_signerAddress, _username, _email);
        wallet.profileRegistered(user.username);
        await _finishWithPrimaryWallet(_signerAddress, user.address);
      });

  @override
  Widget build(BuildContext context) {
    return AuthLayout(
      title: 'Welcome',
      subtitle: 'Set up your non-custodial Trovo Wallet.',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 12), child: Text(_error, style: const TextStyle(color: Colors.red))),
          if (_step == _Step.chooseSigner) ...[
            const Text('Get started'),
            const SizedBox(height: 12),
            AppButton(label: 'Create a new wallet', onPressed: _busy ? null : _handleGenerateNew),
            const SizedBox(height: 8),
            AppButtonSecondary(label: 'Import an existing wallet', onPressed: _busy ? null : _handleImportExisting),
          ],
          if (_step == _Step.createSignerReveal)
            MnemonicReveal(mnemonic: _signerPhrase, onConfirmed: () => setState(() => _step = _Step.signerPassword)),
          if (_step == _Step.importSigner) ...[
            TextField(
              maxLines: 3,
              decoration: const InputDecoration(labelText: 'Signer recovery phrase', border: OutlineInputBorder()),
              onChanged: (v) => _signerPhrase = v,
            ),
            const SizedBox(height: 12),
            AppButton(label: 'Continue', onPressed: _busy || _signerPhrase.isEmpty ? null : _handleImportSignerSubmit),
          ],
          if (_step == _Step.signerPassword) ...[
            const Text('Set a password'),
            const SizedBox(height: 8),
            const Text('This password encrypts your signer wallet on this device. It is never sent anywhere.'),
            const SizedBox(height: 12),
            AppTextInput(label: 'Password', value: _password, obscureText: true, onChanged: (v) => _password = v),
            const SizedBox(height: 12),
            AppTextInput(label: 'Confirm password', value: _confirmPassword, obscureText: true, onChanged: (v) => _confirmPassword = v),
            const SizedBox(height: 12),
            AppButton(label: 'Continue', onPressed: _busy ? null : _handleSignerPasswordSubmit),
          ],
          if (_step == _Step.account) ...[
            const Text('Create your profile'),
            const SizedBox(height: 12),
            AppTextInput(label: 'Username', value: _username, onChanged: (v) => _username = v),
            const SizedBox(height: 12),
            AppTextInput(label: 'Email', value: _email, keyboardType: TextInputType.emailAddress, onChanged: (v) => _email = v),
            const SizedBox(height: 12),
            AppButton(label: 'Continue', onPressed: _busy ? null : _handleAccountSubmit),
          ],
          if (_step == _Step.done) ...[
            const Text("You're all set"),
            const SizedBox(height: 12),
            AppButton(label: 'Go to dashboard', onPressed: () => Navigator.of(context).pushReplacementNamed('/dashboard')),
          ],
        ],
      ),
    );
  }
}
