// Widget test for ReceiptPage rendering a passed-in payment record.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/api/payments_api.dart';
import 'package:wallet_mobile/pages/receipt/receipt_page.dart';

import '../test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('renders receipt fields for a payment record', (tester) async {
    final services = await buildTestServices();
    final record = PaymentHistoryRecord(
      idempotencyKey: 'idem-1',
      fromAddress: '0x1111111111111111111111111111111111111111',
      toAddress: '0x2222222222222222222222222222222222222222',
      tokenAddress: '',
      amount: '1000',
      txHash: '0xabc123',
      createdAt: '2026-01-01T00:00:00Z',
    );

    await tester.pumpWidget(
      MultiProvider(
        providers: [Provider<AppServices>.value(value: services)],
        child: MaterialApp(theme: buildAppTheme(), home: ReceiptPage(record: record)),
      ),
    );
    await tester.pump();

    expect(find.text('Payment receipt'), findsWidgets);
    expect(find.text(record.fromAddress), findsOneWidget);
    expect(find.text(record.toAddress), findsOneWidget);
    expect(find.text(record.txHash), findsOneWidget);
  });
}
