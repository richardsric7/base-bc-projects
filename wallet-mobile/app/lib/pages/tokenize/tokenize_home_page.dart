// Mirrors wallet-web's pages/tokenize/TokenizeHome.tsx - asset
// tokenization hub: the original app's `tokenize/apply` issuer flow and
// `tokenizedAssets`/`subscriptions.mine` investor views, folded into one
// tabbed page. Core deal-terms fields only - detailed asset-class-
// specific fields (bond/fund/commodity documentation, stakeholder
// directory, etc.) are completed with the vetting team after submission.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../store/wallet_state.dart';
import '../../widgets/app_button.dart';
import '../../widgets/app_text_input.dart';
import '../../api/tokenization_api.dart';

class TokenizeHomePage extends StatelessWidget {
  const TokenizeHomePage({super.key});

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 3,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('Asset tokenization'),
          bottom: const TabBar(tabs: [Tab(text: 'My applications'), Tab(text: 'New application'), Tab(text: 'My investments')]),
        ),
        body: TabBarView(
          children: [
            const _MyApplications(),
            _NewApplicationForm(onSubmitted: () => DefaultTabController.of(context).animateTo(0)),
            const _MyInvestments(),
          ],
        ),
      ),
    );
  }
}

class _MyApplications extends StatefulWidget {
  const _MyApplications();

  @override
  State<_MyApplications> createState() => _MyApplicationsState();
}

class _MyApplicationsState extends State<_MyApplications> {
  List<TokenizedAsset>? _applications;
  String _error = '';

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
        .listMyApplications(primaryAddress)
        .then((a) => setState(() => _applications = a))
        .catchError((err) => setState(() => _error = err.toString()));
  }

  Future<void> _delete(int assetId) async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    try {
      await services.tokenization.deleteApplication(primaryAddress, assetId);
      _load();
    } catch (err) {
      setState(() => _error = err.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_error.isNotEmpty) return Padding(padding: const EdgeInsets.all(16), child: Text(_error, style: const TextStyle(color: Colors.red)));
    if (_applications == null) return const Padding(padding: EdgeInsets.all(16), child: Text('Loading…'));
    if (_applications!.isEmpty) return const Padding(padding: EdgeInsets.all(16), child: Text('No applications yet.'));

    return ListView(
      padding: const EdgeInsets.all(16),
      children: _applications!
          .map(
            (a) => Card(
              child: ListTile(
                title: Text(a.assetName.isNotEmpty ? a.assetName : 'Asset #${a.id}'),
                subtitle: Text('${a.assetCode} · ${a.status}'),
                onTap: () => Navigator.of(context).pushNamed('/tokenize/${a.id}'),
                trailing: a.status == 'DRAFT'
                    ? TextButton(onPressed: () => _delete(a.id), child: const Text('Delete', style: TextStyle(color: Colors.red)))
                    : null,
              ),
            ),
          )
          .toList(),
    );
  }
}

class _NewApplicationForm extends StatefulWidget {
  const _NewApplicationForm({required this.onSubmitted});
  final VoidCallback onSubmitted;

  @override
  State<_NewApplicationForm> createState() => _NewApplicationFormState();
}

class _NewApplicationFormState extends State<_NewApplicationForm> {
  String _assetSector = '';
  String _assetSubSector = '';
  String _assetType = '';
  String _assetName = '';
  String _assetCode = '';
  String _assetDescription = '';
  String _assetCountryLocation = 'NG';
  String _assetQuoteCurrency = 'USDC';
  String _numberOfTokenToBeIssued = '';
  String _maxNumberOfTokenAvailableForSale = '';
  String _pricePerToken = '';
  bool _busy = false;
  String _error = '';

  Future<void> _submit() async {
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    setState(() {
      _error = '';
      _busy = true;
    });
    try {
      await services.tokenization.submitApplication(
        primaryAddress,
        NewApplicationInput(
          assetSector: _assetSector,
          assetSubSector: _assetSubSector,
          assetType: _assetType,
          assetName: _assetName,
          assetCode: _assetCode,
          assetDescription: _assetDescription,
          assetCountryLocation: _assetCountryLocation,
          assetQuoteCurrency: _assetQuoteCurrency,
          numberOfTokenToBeIssued: _numberOfTokenToBeIssued,
          maxNumberOfTokenAvailableForSale: _maxNumberOfTokenAvailableForSale,
          pricePerToken: _pricePerToken,
        ),
      );
      widget.onSubmitted();
    } catch (err) {
      setState(() => _error = err.toString());
    } finally {
      setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        const Text(
          'Core deal terms only - detailed asset-class-specific fields (bond/fund/commodity documentation, stakeholder directory, etc.) are completed with the vetting team after submission.',
        ),
        const SizedBox(height: 12),
        AppTextInput(label: 'Asset name', value: _assetName, hint: 'Trovo Gold Trust', onChanged: (v) => setState(() => _assetName = v)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Asset code', value: _assetCode, hint: 'TGLD', onChanged: (v) => setState(() => _assetCode = v)),
        const SizedBox(height: 8),
        AppTextInput(label: 'Description', value: _assetDescription, hint: 'A gold-backed asset', onChanged: (v) => _assetDescription = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Sector', value: _assetSector, hint: 'COMMODITY', onChanged: (v) => _assetSector = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Sub-sector', value: _assetSubSector, hint: 'PRECIOUS_METALS', onChanged: (v) => _assetSubSector = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Type', value: _assetType, hint: 'GOLD', onChanged: (v) => _assetType = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Country (ISO 2-letter)', value: _assetCountryLocation, hint: 'NG', onChanged: (v) => _assetCountryLocation = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Quote currency', value: _assetQuoteCurrency, hint: 'USDC', onChanged: (v) => _assetQuoteCurrency = v),
        const SizedBox(height: 8),
        AppTextInput(label: 'Tokens to issue', value: _numberOfTokenToBeIssued, hint: '1000', onChanged: (v) => _numberOfTokenToBeIssued = v),
        const SizedBox(height: 8),
        AppTextInput(
          label: 'Tokens available for sale',
          value: _maxNumberOfTokenAvailableForSale,
          hint: '600',
          onChanged: (v) => _maxNumberOfTokenAvailableForSale = v,
        ),
        const SizedBox(height: 8),
        AppTextInput(label: 'Price per token', value: _pricePerToken, hint: '2.50', onChanged: (v) => _pricePerToken = v),
        if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 8), child: Text(_error, style: const TextStyle(color: Colors.red))),
        const SizedBox(height: 12),
        AppButton(
          label: _busy ? 'Submitting…' : 'Submit application',
          onPressed: _busy || _assetName.isEmpty || _assetCode.isEmpty ? null : _submit,
        ),
      ],
    );
  }
}

class _MyInvestments extends StatefulWidget {
  const _MyInvestments();

  @override
  State<_MyInvestments> createState() => _MyInvestmentsState();
}

class _MyInvestmentsState extends State<_MyInvestments> {
  List<TokenizedAssetSubscription> _subscriptions = [];
  List<ExpressionOfInterest> _interest = [];
  List<TokenizedAssetEarlyExit> _exits = [];
  String _error = '';
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    final services = context.read<AppServices>();
    final primaryAddress = context.read<WalletState>().primary.address;
    if (primaryAddress == null) return;
    Future.wait([
      services.tokenization.listMySubscriptions(primaryAddress),
      services.tokenization.listMyInterest(primaryAddress),
      services.tokenization.listMyEarlyExits(primaryAddress),
    ]).then((results) {
      setState(() {
        _subscriptions = results[0] as List<TokenizedAssetSubscription>;
        _interest = results[1] as List<ExpressionOfInterest>;
        _exits = results[2] as List<TokenizedAssetEarlyExit>;
        _loaded = true;
      });
    }).catchError((err) => setState(() => _error = err.toString()));
  }

  @override
  Widget build(BuildContext context) {
    if (_error.isNotEmpty) return Padding(padding: const EdgeInsets.all(16), child: Text(_error, style: const TextStyle(color: Colors.red)));
    if (!_loaded) return const Padding(padding: EdgeInsets.all(16), child: Text('Loading…'));

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        const Text('Purchases', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        if (_subscriptions.isEmpty)
          const Text('None yet.')
        else
          ..._subscriptions.map((s) => Text('${s.quantity} tokens (asset #${s.tokenizedAssetId}) for ${s.paymentAmount} ${s.paymentAssetSymbol} via ${s.channel}')),
        const SizedBox(height: 24),
        const Text('Expressions of interest', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        if (_interest.isEmpty)
          const Text('None yet.')
        else
          ..._interest.map((i) => Text('Asset #${i.tokenizedAssetId}${i.amount != null ? ' - ${i.amount}' : ''}')),
        const SizedBox(height: 24),
        const Text('Early exits', style: TextStyle(fontWeight: FontWeight.bold)),
        const SizedBox(height: 8),
        if (_exits.isEmpty)
          const Text('None yet.')
        else
          ..._exits.map((e) => Text('Asset #${e.tokenizedAssetId}: ${e.tokenQuantityExited} tokens, est. payout ${e.estimatedPayoutAmount} ${e.payoutCurrency}')),
      ],
    );
  }
}
