// Mirrors wallet-web's pages/receipt/Receipt.tsx - a printable payment
// receipt (wallet-web/PLAN.md §19's closure of the original app's
// `pdfPages/sendAssetReceipt.tsx` gap). Reached from send_page.dart's
// success state or a dashboard history row, passed the record directly
// as a constructor argument (the mobile counterpart of wallet-web's
// router-state hand-off - there is no URL/query-param layer here to
// round-trip through). No print-to-PDF equivalent exists on-device, so
// this simply renders the same fields as an ordinary page.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../app_services.dart';
import '../../api/payments_api.dart';

String _explorerUrl(int chainId, String txHash) {
  final base = chainId == 8453 ? 'https://basescan.org/tx/' : 'https://sepolia.basescan.org/tx/';
  return base + txHash;
}

class ReceiptPage extends StatelessWidget {
  const ReceiptPage({super.key, required this.record});

  final PaymentHistoryRecord record;

  @override
  Widget build(BuildContext context) {
    final chainId = context.read<AppServices>().network.getNetworkConfig().chainId;
    final explorerUrl = _explorerUrl(chainId, record.txHash);

    return Scaffold(
      appBar: AppBar(title: const Text('Payment receipt')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Center(
            child: Column(
              children: [
                Text('Payment receipt', style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 4),
                Text('Generated ${DateTime.now()}', style: const TextStyle(color: Colors.grey, fontSize: 12)),
              ],
            ),
          ),
          const SizedBox(height: 24),
          Container(
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(color: const Color(0xFFF2F6F9), borderRadius: BorderRadius.circular(16)),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _Row(label: 'From', value: record.fromAddress),
                _Row(label: 'To', value: record.toAddress),
                _Row(label: 'Amount (base units)', value: record.amount),
                if (record.tokenAddress.isNotEmpty) _Row(label: 'Token', value: record.tokenAddress),
                if (record.memo.isNotEmpty) _Row(label: 'Memo', value: record.memo),
                _Row(label: 'Transaction hash', value: record.txHash, link: explorerUrl),
                _Row(label: 'Date', value: record.createdAt),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({required this.label, required this.value, this.link});
  final String label;
  final String value;
  final String? link;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label.toUpperCase(), style: const TextStyle(fontSize: 11, fontWeight: FontWeight.bold, color: Colors.grey)),
          const SizedBox(height: 2),
          Text(
            value,
            style: link != null
                ? const TextStyle(fontSize: 13, decoration: TextDecoration.underline, color: Colors.blue)
                : const TextStyle(fontSize: 13),
          ),
        ],
      ),
    );
  }
}
