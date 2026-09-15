// Widget test for SwapPage in its idle state (no network call triggered -
// only rendering is exercised here). Mirrors send_page_test.dart.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/store/wallet_state.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/pages/swap/swap_page.dart';

import '../test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('renders the swap form with router/method/args fields', (tester) async {
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
        child: MaterialApp(theme: buildAppTheme(), home: const SwapPage()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Swap'), findsWidgets);
    expect(find.text('Router address'), findsOneWidget);
    expect(find.text('Method'), findsOneWidget);
    expect(find.text('Value (wei)'), findsOneWidget);
  });
}
