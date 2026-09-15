// Mirrors wallet-web's pages/tokenize/AssetDetail.tsx - a single
// tokenized asset: issuer-side application actions (confirm, pay the
// application fee, confirm fee payment once vetted) and investor-side
// actions (subscribe with crypto, express interest, early exit). Fee
// payment and crypto purchase/early exit all sign a real digest, same
// pattern as send_page.dart.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../api/tokenization_api.dart';

class AssetDetailPage extends StatefulWidget {
  const AssetDetailPage({super.key, required this.assetId});
  final int assetId;

  @override
  State<AssetDetailPage> createState() => _AssetDetailPageState();
}

class _AssetDetailPageState extends State<AssetDetailPage> {
  TokenizedAsset? _asset;
  String _error = '';
  String _notice = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    services.tokenization
        .getApplication(primaryAddress, widget.assetId)
        .then((a) => setState(() => _asset = a))
        .catchError((err) => setState(() => _error = err.toString()));
  }

  Future<void> _confirmApplication() async {
    final services = context.read<AppServices>();
    final wallet = context.read<WalletState>();
    final primaryAddress = wallet.primary.address;
    if (primaryAddress == null) return;
    setState(() => _error = '');
    try {
      final feePayment = await services.tokenization.confirmApplication(primaryAddress, widget.assetId);
      if (feePayment != null) {
        final signature = await services.walletCore.signHexDigest('signer', feePayment.digestToSign);
        await services.tokenization.submitApplicationFee(primaryAddress, widget.assetId, feePayment.actionId, signature);
      }
      setState(() => _notice = 'Application confirmed.');
      _load();
    } catch (err) {
      setState(() => _error = err.toString());
    }
  }

  Future<void> _confirmFeePayment() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() => _error = '');
    try {
      await services.tokenization.confirmFeePayment(primaryAddress, widget.assetId);
      setState(() => _notice = 'Fee payment confirmed.');
      _load();
    } catch (err) {
      setState(() => _error = err.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    final asset = _asset;
    if (asset == null) {
      return Scaffold(
        appBar: AppBar(title: const Text('Asset')),
        body: Padding(padding: const EdgeInsets.all(16), child: Text(_error.isNotEmpty ? _error : 'Loading…')),
      );
    }

    final onSale = asset.status == 'PRIMARY_SALE_ACTIVE' || asset.status == 'SECONDARY_SALE_ACTIVE';

    return Scaffold(
      appBar: AppBar(title: Text(asset.assetName.isNotEmpty ? asset.assetName : 'Asset #${asset.id}')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text('${asset.assetCode} · ${asset.status}${asset.vettingStatus ? ' · vetted' : ''}', style: const TextStyle(color: Colors.grey)),
          const SizedBox(height: 8),
          Text(asset.assetDescription),
          const SizedBox(height: 4),
          Text('Price per token: ${asset.pricePerToken} ${asset.assetQuoteCurrency}'),
          const SizedBox(height: 16),
          if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
          if (_notice.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 8), child: Text(_notice, style: const TextStyle(color: Colors.green))),
          if (asset.status == 'DRAFT') AppButton(label: 'Confirm application', onPressed: _confirmApplication),
          if (asset.status == 'APPLICATION_CONFIRMED' && asset.vettingStatus) AppButton(label: 'Confirm fee payment', onPressed: _confirmFeePayment),
          if (asset.status == 'APPLICATION_CONFIRMED' && !asset.vettingStatus) const Text('Awaiting vetting before fee payment can be confirmed.'),
          if (onSale) ...[
            _SubscribeSection(assetId: asset.id, onDone: (m) => setState(() => _notice = m), onError: (m) => setState(() => _error = m)),
            const Divider(height: 32),
            _InterestSection(assetId: asset.id, onDone: (m) => setState(() => _notice = m), onError: (m) => setState(() => _error = m)),
            const Divider(height: 32),
            _EarlyExitSection(assetId: asset.id, onDone: (m) => setState(() => _notice = m), onError: (m) => setState(() => _error = m)),
          ],
        ],
      ),
    );
  }
}

class _SubscribeSection extends StatefulWidget {
  const _SubscribeSection({required this.assetId, required this.onDone, required this.onError});
  final int assetId;
  final void Function(String) onDone;
  final void Function(String) onError;

  @override
  State<_SubscribeSection> createState() => _SubscribeSectionState();
}

class _SubscribeSectionState extends State<_SubscribeSection> {
  String _quantity = '';
  bool _busy = false;

  Future<void> _submit() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() => _busy = true);
    try {
      final proposal = await services.tokenization.buildCryptoPurchase(primaryAddress, widget.assetId, _quantity);
      final signature = await services.walletCore.signHexDigest('signer', proposal.digestToSign);
      await services.tokenization.confirmCryptoPurchase(primaryAddress, widget.assetId, _quantity, proposal.actionId, signature);
      widget.onDone('Purchase submitted.');
      setState(() => _quantity = '');
    } catch (err) {
      widget.onError(err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Subscribe (crypto)', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Quantity', value: _quantity, hint: '10', onChanged: (v) => setState(() => _quantity = v)),
        const SizedBox(height: 8),
        AppButton(label: _busy ? 'Submitting…' : 'Buy', onPressed: _busy || _quantity.isEmpty ? null : _submit),
      ],
    );
  }
}

class _InterestSection extends StatefulWidget {
  const _InterestSection({required this.assetId, required this.onDone, required this.onError});
  final int assetId;
  final void Function(String) onDone;
  final void Function(String) onError;

  @override
  State<_InterestSection> createState() => _InterestSectionState();
}

class _InterestSectionState extends State<_InterestSection> {
  String _amount = '';
  bool _busy = false;

  Future<void> _submit() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() => _busy = true);
    try {
      await services.tokenization.expressInterest(primaryAddress, widget.assetId, amount: _amount.isEmpty ? null : _amount);
      widget.onDone('Interest recorded.');
    } catch (err) {
      widget.onError(err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Express interest', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Indicative amount (optional)', value: _amount, hint: '500', onChanged: (v) => _amount = v),
        const SizedBox(height: 8),
        AppButton(label: _busy ? 'Submitting…' : 'Express interest', onPressed: _busy ? null : _submit),
      ],
    );
  }
}

class _EarlyExitSection extends StatefulWidget {
  const _EarlyExitSection({required this.assetId, required this.onDone, required this.onError});
  final int assetId;
  final void Function(String) onDone;
  final void Function(String) onError;

  @override
  State<_EarlyExitSection> createState() => _EarlyExitSectionState();
}

class _EarlyExitSectionState extends State<_EarlyExitSection> {
  String _quantity = '';
  String _bankId = '';
  String _accountNumber = '';
  String _accountName = '';
  bool _busy = false;

  Future<void> _submit() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() => _busy = true);
    try {
      final proposal = await services.tokenization.buildEarlyExit(primaryAddress, widget.assetId, _quantity);
      final signature = await services.walletCore.signHexDigest('signer', proposal.digestToSign);
      await services.tokenization.confirmEarlyExit(
        primaryAddress,
        widget.assetId,
        _quantity,
        int.tryParse(_bankId) ?? 0,
        _accountNumber,
        _accountName,
        proposal.actionId,
        signature,
      );
      widget.onDone('Early exit submitted.');
      setState(() => _quantity = '');
    } catch (err) {
      widget.onError(err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Early exit', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Quantity', value: _quantity, hint: '5', onChanged: (v) => _quantity = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Bank ID', value: _bankId, hint: '1', onChanged: (v) => _bankId = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Account number', value: _accountNumber, hint: '0123456789', onChanged: (v) => _accountNumber = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Account name', value: _accountName, hint: 'Jane Doe', onChanged: (v) => _accountName = v),
        const SizedBox(height: 8),
        AppButton(
          label: _busy ? 'Submitting…' : 'Exit early',
          onPressed: _busy || _quantity.isEmpty || _bankId.isEmpty || _accountNumber.isEmpty || _accountName.isEmpty ? null : _submit,
        ),
      ],
    );
  }
}
