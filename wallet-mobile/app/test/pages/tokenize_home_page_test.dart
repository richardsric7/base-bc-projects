// Widget test for TokenizeHomePage in its idle state.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/store/wallet_state.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/pages/tokenize/tokenize_home_page.dart';

import '../test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('renders the tokenize tabs', (tester) async {
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
        child: MaterialApp(theme: buildAppTheme(), home: const TokenizeHomePage()),
      ),
    );
    await tester.pump();

    expect(find.text('Asset tokenization'), findsOneWidget);
    expect(find.text('My applications'), findsOneWidget);
    expect(find.text('New application'), findsOneWidget);
    expect(find.text('My investments'), findsOneWidget);
  });
}
