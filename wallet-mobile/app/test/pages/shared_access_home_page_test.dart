// Widget test for SharedAccessHomePage in its idle state.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/store/wallet_state.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/pages/sharedaccess/shared_access_home_page.dart';

import '../test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('renders the shared-access tabs', (tester) async {
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
        child: MaterialApp(theme: buildAppTheme(), home: const SharedAccessHomePage()),
      ),
    );
    await tester.pump();

    expect(find.text('Shared access'), findsOneWidget);
    expect(find.text('My wallets'), findsOneWidget);
    expect(find.text('Create group'), findsOneWidget);
    expect(find.text('Approvals'), findsOneWidget);
  });
}
