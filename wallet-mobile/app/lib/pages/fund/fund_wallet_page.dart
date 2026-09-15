// Mirrors wallet-web's pages/fund/FundWallet.tsx - fiat/stablerail/crypto
// wallet-funding hub (wallet-web/PLAN.md §19's closure of the "Deposit/
// Withdraw" gap). Withdrawal is the one path here that moves the user's
// own on-chain funds, so it alone follows the build -> signHexDigest ->
// confirm pattern; fiat/stablerail top-ups and crypto deposits never
// touch the user's signer key at all.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../api/stablerail_api.dart';
import '../../api/crypto_api.dart';

enum _Tab { fiat, naira, crypto }

class FundWalletPage extends StatefulWidget {
  const FundWalletPage({super.key});

  @override
  State<FundWalletPage> createState() => _FundWalletPageState();
}

class _FundWalletPageState extends State<FundWalletPage> {
  _Tab _tab = _Tab.fiat;

  String _tabLabel(_Tab t) {
    switch (t) {
      case _Tab.fiat:
        return 'Card / Bank (Flutterwave)';
      case _Tab.naira:
        return 'Naira (Stablerail)';
      case _Tab.crypto:
        return 'Crypto';
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Fund wallet')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Wrap(
            spacing: 8,
            children: _Tab.values
                .map((t) => ChoiceChip(label: Text(_tabLabel(t)), selected: _tab == t, onSelected: (_) => setState(() => _tab = t)))
                .toList(),
          ),
          const SizedBox(height: 16),
          if (_tab == _Tab.fiat) const _FiatTab(),
          if (_tab == _Tab.naira) const _NairaTab(),
          if (_tab == _Tab.crypto) const _CryptoTab(),
        ],
      ),
    );
  }
}

class _FiatTab extends StatefulWidget {
  const _FiatTab();

  @override
  State<_FiatTab> createState() => _FiatTabState();
}

class _FiatTabState extends State<_FiatTab> {
  String _amount = '';
  String _currency = 'NGN';
  bool _busy = false;
  String _error = '';
  String _invoiceRef = '';

  Future<void> _handleTopUp() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() {
      _error = '';
      _busy = true;
    });
    try {
      final reference = DateTime.now().microsecondsSinceEpoch.toString();
      final invoice = await services.fiat.createFiatInvoice(primaryAddress, reference, 'topup', num.parse(_amount), _currency);
      setState(() => _invoiceRef = invoice.reference);
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Top up via Flutterwave (card, bank transfer, or mobile money).', style: TextStyle(color: Colors.grey)),
        const SizedBox(height: 12),
        AppTextInput(label: 'Amount', value: _amount, hint: '50', onChanged: (v) => setState(() => _amount = v)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Currency', value: _currency, hint: 'NGN', onChanged: (v) => setState(() => _currency = v)),
        if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
        if (_invoiceRef.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text('Invoice created ($_invoiceRef) - complete payment via the emailed Flutterwave link.', style: const TextStyle(color: Colors.green)),
          ),
        const SizedBox(height: 12),
        AppButton(label: _busy ? 'Creating invoice…' : 'Create invoice', onPressed: _busy || _amount.isEmpty ? null : _handleTopUp),
      ],
    );
  }
}

class _NairaTab extends StatefulWidget {
  const _NairaTab();

  @override
  State<_NairaTab> createState() => _NairaTabState();
}

class _NairaTabState extends State<_NairaTab> {
  String _bvn = '';
  String _amount = '';
  List<Bank>? _banks;
  bool _busy = false;
  String _error = '';
  String _message = '';

  Future<void> _loadBanks() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    try {
      final banks = await services.stablerail.listBanks(primaryAddress);
      setState(() => _banks = banks);
    } catch (err) {
      setState(() => _error = err.toString());
    }
  }

  Future<void> _handleOnboard() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() {
      _error = '';
      _busy = true;
    });
    try {
      final message = await services.stablerail.initiateOnboarding(primaryAddress, _bvn);
      setState(() => _message = message);
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  Future<void> _handleOnramp() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() {
      _error = '';
      _busy = true;
    });
    try {
      await services.stablerail.initiateOnramp(primaryAddress, num.parse(_amount));
      setState(() => _message = 'Onramp initiated - cNGN will credit your wallet once the transfer clears.');
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Text('Onboard your BVN once, then fund via bank transfer to receive cNGN.', style: TextStyle(color: Colors.grey)),
        const SizedBox(height: 8),
        AppTextInput(label: 'BVN (11 digits)', value: _bvn, hint: '12345678901', onChanged: (v) => setState(() => _bvn = v)),
        const SizedBox(height: 8),
        AppButton(label: _busy ? 'Submitting…' : 'Onboard BVN', onPressed: _busy || _bvn.length != 11 ? null : _handleOnboard),
        const SizedBox(height: 16),
        AppTextInput(label: 'Amount (NGN)', value: _amount, hint: '5000', onChanged: (v) => setState(() => _amount = v)),
        const SizedBox(height: 8),
        AppButton(label: _busy ? 'Submitting…' : 'Initiate onramp', onPressed: _busy || _amount.isEmpty ? null : _handleOnramp),
        const SizedBox(height: 12),
        TextButton(onPressed: _loadBanks, child: const Text('Show supported banks')),
        if (_banks != null)
          ..._banks!.map((b) => Text('• ${b.name}', style: const TextStyle(color: Colors.grey))),
        if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
        if (_message.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_message, style: const TextStyle(color: Colors.green))),
      ],
    );
  }
}

class _CryptoTab extends StatefulWidget {
  const _CryptoTab();

  @override
  State<_CryptoTab> createState() => _CryptoTabState();
}

class _CryptoTabState extends State<_CryptoTab> {
  String _currency = 'USDC';
  List<DepositAddress>? _depositAddresses;
  String _error = '';

  String _network = 'base';
  String _amount = '';
  String _toAddress = '';
  WithdrawalNetworksResult? _networksResult;
  _WithdrawStatus _status = _WithdrawStatus.idle;
  String _withdrawTxHash = '';

  bool get _busy => _status != _WithdrawStatus.idle && _status != _WithdrawStatus.done;

  Future<void> _loadDeposit() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() => _error = '');
    try {
      final addresses = await services.crypto.getDepositAddresses(primaryAddress, _currency);
      setState(() => _depositAddresses = addresses);
    } catch (err) {
      setState(() => _error = err.toString());
    }
  }

  Future<void> _loadNetworks() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() => _error = '');
    try {
      final result = await services.crypto.getWithdrawalNetworks(primaryAddress, _currency);
      setState(() => _networksResult = result);
    } catch (err) {
      setState(() => _error = err.toString());
    }
  }

  Future<void> _handleWithdraw() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() {
      _error = '';
      _withdrawTxHash = '';
    });
    try {
      setState(() => _status = _WithdrawStatus.building);
      final proposal = await services.crypto.buildWithdrawal(primaryAddress, _currency, _network, num.parse(_amount));

      setState(() => _status = _WithdrawStatus.signing);
      final signature = await services.walletCore.signHexDigest('signer', proposal.digestToSign);

      setState(() => _status = _WithdrawStatus.submitting);
      final request = await services.crypto.confirmWithdrawal(
        primaryAddress,
        _currency,
        _network,
        _toAddress,
        num.parse(_amount),
        proposal.actionId,
        signature,
      );
      setState(() {
        _withdrawTxHash = request.treasuryTxHash;
        _status = _WithdrawStatus.done;
      });
    } catch (err) {
      setState(() {
        _error = err.toString();
        _status = _WithdrawStatus.idle;
      });
    }
  }

  String _statusLabel() {
    switch (_status) {
      case _WithdrawStatus.building:
        return 'Building transaction…';
      case _WithdrawStatus.signing:
        return 'Signing…';
      case _WithdrawStatus.submitting:
        return 'Submitting…';
      default:
        return 'Withdraw';
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        AppTextInput(label: 'Currency', value: _currency, hint: 'USDC', onChanged: (v) => setState(() => _currency = v)),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          children: [
            TextButton(onPressed: _loadDeposit, child: const Text('Show deposit address')),
            TextButton(onPressed: _loadNetworks, child: const Text('Show withdrawal networks')),
          ],
        ),
        if (_depositAddresses != null && _depositAddresses!.isNotEmpty)
          Text(
            'Deposit address (${_depositAddresses![0].network}): ${_depositAddresses![0].address}',
            style: const TextStyle(color: Colors.grey),
          ),
        if (_networksResult != null)
          Text(
            'Treasury: ${_networksResult!.treasuryAddress} · Networks: ${_networksResult!.networks.map((n) => n.network).join(', ')}',
            style: const TextStyle(color: Colors.grey),
          ),
        const SizedBox(height: 16),
        const Text('Withdraw to an external address (signs a real transfer from your wallet).', style: TextStyle(color: Colors.grey)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Network', value: _network, hint: 'base', onChanged: (v) => setState(() => _network = v)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Amount', value: _amount, hint: '10', onChanged: (v) => setState(() => _amount = v)),
        const SizedBox(height: 8),
        AppTextInput(
          label: 'Destination address',
          value: _toAddress,
          hint: '0x... or bank details',
          onChanged: (v) => setState(() => _toAddress = v),
        ),
        if (_withdrawTxHash.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text('Withdrawal submitted. Tx hash: $_withdrawTxHash', style: const TextStyle(color: Colors.green)),
          ),
        if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
        const SizedBox(height: 8),
        AppButton(
          label: _busy ? _statusLabel() : 'Withdraw',
          onPressed: _busy || _amount.isEmpty || _toAddress.isEmpty ? null : _handleWithdraw,
        ),
      ],
    );
  }
}

enum _WithdrawStatus { idle, building, signing, submitting, done }
