// Mirrors wallet-web's pages/swap/Swap.tsx exactly - same digest-sign
// flow as send_page.dart, and the same generic router-call shape (no
// hardcoded DEX): wallet-backend's swaps component builds a call against
// whatever router contract/ABI/method the caller supplies.
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';

enum _Status { idle, building, signing, submitting, done }

class SwapPage extends StatefulWidget {
  const SwapPage({super.key});

  @override
  State<SwapPage> createState() => _SwapPageState();
}

class _SwapPageState extends State<SwapPage> {
  String _routerAddress = '';
  String _routerAbi = '';
  String _method = '';
  String _argsJson = '[]';
  String _valueWei = '0';
  _Status _status = _Status.idle;
  String _error = '';
  String _txHash = '';

  bool get _busy => _status != _Status.idle && _status != _Status.done;

  Future<void> _handleSwap() async {
    final services = context.read<AppServices>();
    final wallet = context.read<WalletState>();
    final primaryAddress = wallet.primary.address;
    final signerAddress = wallet.signer.address;
    if (primaryAddress == null || signerAddress == null || !wallet.signer.isUnlocked) {
      setState(() => _error = 'Unlock your signer wallet before swapping.');
      return;
    }
    setState(() {
      _error = '';
      _txHash = '';
    });
    try {
      final args = _argsJson.trim().isEmpty ? <dynamic>[] : (jsonDecode(_argsJson) as List<dynamic>);

      setState(() => _status = _Status.building);
      // Proposes the router call as a real Safe transaction against the
      // primary wallet and returns the digest to approve it. Wrapped so
      // an undeployed Safe (no shared-access group yet) is deployed on
      // the spot and the build is retried.
      final proposal = await services.users.withWalletDeployRetry(
        signerAddress,
        () => services.swaps.buildSwap(
          primaryAddress,
          routerAddress: _routerAddress,
          routerAbi: _routerAbi,
          method: _method,
          args: args,
          valueWei: _valueWei,
        ),
      );

      setState(() => _status = _Status.signing);
      // personal_sign over the digest's raw bytes with the signer's own
      // key - never the (nonexistent) primary wallet key. Must be
      // signHexDigest, not signRequestMessage.
      final signature = await services.walletCore.signHexDigest('signer', proposal.digestToSign);

      setState(() => _status = _Status.submitting);
      final hash = await services.swaps.submitSwap(primaryAddress, proposal.actionId, signature);

      setState(() {
        _txHash = hash;
        _status = _Status.done;
      });
    } catch (err) {
      setState(() {
        _error = err.toString();
        _status = _Status.idle;
      });
    }
  }

  String _statusLabel() {
    switch (_status) {
      case _Status.building:
        return 'Building transaction…';
      case _Status.signing:
        return 'Signing…';
      case _Status.submitting:
        return 'Submitting…';
      default:
        return 'Swap';
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Swap')),
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            AppTextInput(label: 'Router address', value: _routerAddress, hint: '0x...', onChanged: (v) => _routerAddress = v),
            const SizedBox(height: 12),
            const Text('Router ABI (JSON)'),
            const SizedBox(height: 4),
            TextField(
              maxLines: 4,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              onChanged: (v) => _routerAbi = v,
              decoration: const InputDecoration(border: OutlineInputBorder()),
            ),
            const SizedBox(height: 12),
            AppTextInput(label: 'Method', value: _method, hint: 'swapExactTokensForTokens', onChanged: (v) => _method = v),
            const SizedBox(height: 12),
            const Text('Arguments (JSON array)'),
            const SizedBox(height: 4),
            TextField(
              maxLines: 3,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              controller: TextEditingController(text: _argsJson),
              onChanged: (v) => _argsJson = v,
              decoration: const InputDecoration(border: OutlineInputBorder()),
            ),
            const SizedBox(height: 12),
            AppTextInput(label: 'Value (wei)', value: _valueWei, onChanged: (v) => _valueWei = v),
            const SizedBox(height: 16),
            if (_error.isNotEmpty)
              Padding(padding: const EdgeInsets.only(bottom: 12), child: Text(_error, style: const TextStyle(color: Colors.red))),
            if (_status == _Status.done)
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Text('Swap submitted. Tx hash: $_txHash', style: const TextStyle(color: Colors.green)),
              ),
            AppButton(
              label: _busy ? _statusLabel() : 'Swap',
              onPressed: _busy || _routerAddress.isEmpty || _method.isEmpty ? null : _handleSwap,
            ),
          ],
        ),
      ),
    );
  }
}
