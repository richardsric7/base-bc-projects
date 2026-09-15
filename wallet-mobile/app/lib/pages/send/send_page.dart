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
  // What `_destination` actually resolved to (it may be an address,
  // username, email, or wallet alias) - shown once /build responds so
  // the sender can confirm who they're paying before signing.
  String _resolvedAddress = '';

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
      _resolvedAddress = '';
    });
    try {
      setState(() => _status = _Status.building);
      // Proposes the transfer as a real Safe transaction and returns the
      // digest to approve it. Wrapped so an undeployed Safe (no
      // shared-access group yet) is deployed on the spot and the build
      // is retried. _destination may be an address, username, email, or
      // wallet alias - the backend resolves it and returns what it
      // resolved to as resolvedAddress.
      final proposal = await services.users.withWalletDeployRetry(
        signerAddress,
        () => services.payments.buildPayment(primaryAddress, _destination, _amount),
      );
      if (proposal.resolvedAddress != null) {
        setState(() => _resolvedAddress = proposal.resolvedAddress!);
      }

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
        // The actual address, not whatever _destination was typed as
        // (username/email/alias/address) - the history record's
        // toAddress is a display field, and the real address is what a
        // receipt should show.
        proposal.resolvedAddress ?? _destination,
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
            AppTextInput(
              label: 'Recipient',
              value: _destination,
              hint: 'Address, username, email, or wallet alias',
              onChanged: (v) => _destination = v,
            ),
            if (_resolvedAddress.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 4, bottom: 4),
                child: Text('Sending to: $_resolvedAddress', style: const TextStyle(color: Colors.grey, fontSize: 12)),
              ),
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
