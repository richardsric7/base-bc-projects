// Mirrors wallet-web's pages/send/Send.tsx exactly - same
// build/sign/submit shape, same withWalletDeployRetry backstop, same
// signHexDigest (NOT signRequestMessage) for the approval signature.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';

enum _Status { idle, building, signing, submitting, done }

class SendPage extends StatefulWidget {
  const SendPage({super.key});

  @override
  State<SendPage> createState() => _SendPageState();
}

class _SendPageState extends State<SendPage> {
  String _destination = '';
  String _amount = '';
  _Status _status = _Status.idle;
  String _error = '';
  String _txHash = '';

  bool get _busy => _status != _Status.idle && _status != _Status.done;

  Future<void> _handleSend() async {
    final services = context.read<AppServices>();
    final wallet = context.read<WalletState>();
    final primaryAddress = wallet.primary.address;
    final signerAddress = wallet.signer.address;
    if (primaryAddress == null || signerAddress == null || !wallet.signer.isUnlocked) {
      setState(() => _error = 'Unlock your signer wallet before sending a payment.');
      return;
    }
    setState(() {
      _error = '';
      _txHash = '';
    });
    try {
      setState(() => _status = _Status.building);
      // Proposes the transfer as a real Safe transaction and returns the
      // digest to approve it. Wrapped so an undeployed Safe (no
      // shared-access group yet) is deployed on the spot and the build
      // is retried.
      final proposal = await services.users.withWalletDeployRetry(
        signerAddress,
        () => services.payments.buildPayment(primaryAddress, _destination, _amount),
      );

      setState(() => _status = _Status.signing);
      // personal_sign over the digest's raw bytes with the signer's own
      // key - never the (nonexistent) primary wallet key. Must be
      // signHexDigest, not signRequestMessage.
      final signature = await services.walletCore.signHexDigest('signer', proposal.digestToSign);

      setState(() => _status = _Status.submitting);
      final idempotencyKey = DateTime.now().microsecondsSinceEpoch.toString();
      final record = await services.payments.submitPayment(
        primaryAddress,
        idempotencyKey,
        proposal.actionId,
        signature,
        _destination,
        _amount,
      );

      setState(() {
        _txHash = record.txHash;
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
        return 'Send payment';
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Send')),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            AppTextInput(label: 'Recipient address', value: _destination, onChanged: (v) => _destination = v),
            const SizedBox(height: 12),
            AppTextInput(
              label: 'Amount (base units)',
              value: _amount,
              keyboardType: TextInputType.number,
              onChanged: (v) => _amount = v,
            ),
            const SizedBox(height: 16),
            if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 12), child: Text(_error, style: const TextStyle(color: Colors.red))),
            if (_status == _Status.done)
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Text('Payment submitted. Tx hash: $_txHash', style: const TextStyle(color: Colors.green)),
              ),
            AppButton(label: _busy ? _statusLabel() : 'Send payment', onPressed: _busy ? null : _handleSend),
          ],
        ),
      ),
    );
  }
}
