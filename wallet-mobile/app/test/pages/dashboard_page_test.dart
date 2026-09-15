// Widget + golden test for the dashboard's navigation drawer, whose
// header carries the real production mobile app's white logo mark on a
// trovoblue background (PLAN.md §14.5) - see test_helpers.dart for the
// real wallet-core FFI bridge used elsewhere in these tests.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/store/wallet_state.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/pages/dashboard/dashboard_page.dart';

import '../test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('drawer header shows the brand mark on a trovoblue background', (tester) async {
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
        child: MaterialApp(theme: buildAppTheme(), home: const DashboardPage()),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('Open navigation menu'));
    await tester.pumpAndSettle();

    // Same fake-async escape hatch as onboarding_wizard_test.dart's
    // choose-signer golden: Image.asset decode needs a real event loop.
    await tester.runAsync(() => precacheImage(const AssetImage('assets/images/trovo_white.png'), tester.element(find.byType(Drawer))));
    await tester.pumpAndSettle();

    expect(find.text('Send'), findsOneWidget);
    expect(find.text('Settings'), findsOneWidget);

    await expectLater(find.byType(Drawer), matchesGoldenFile('../goldens/dashboard_drawer.png'));
  });
}
