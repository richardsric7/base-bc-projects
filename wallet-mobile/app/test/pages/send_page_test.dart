// Widget + golden test for SendPage in its idle state (no network call
// triggered - only rendering is exercised here).
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/store/wallet_state.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/pages/send/send_page.dart';

import '../test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('renders the send form with recipient/amount fields', (tester) async {
    final services = await buildTestServices();
    final wallet = WalletState();
    wallet.hydrate(
      signer: const RoleState(address: '0x1111111111111111111111111111111111111111', hasVault: true, isUnlocked: true),
      primary: const RoleState(address: '0x2222222222222222222222222222222222222222', hasVault: true, isUnlocked: true),
    );

    await tester.pumpWidget(
      MultiProvider(
        providers: [
          Provider<AppServices>.value(value: services),
          ChangeNotifierProvider<WalletState>.value(value: wallet),
        ],
        child: MaterialApp(theme: buildAppTheme(), home: const SendPage()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Send'), findsWidgets);
    expect(find.text('Recipient'), findsOneWidget);
    expect(find.text('Amount (base units)'), findsOneWidget);
    expect(find.text('Send payment'), findsOneWidget);

    await expectLater(find.byType(SendPage), matchesGoldenFile('../goldens/send_page_idle.png'));
  });
}
