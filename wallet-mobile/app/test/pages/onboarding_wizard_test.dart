// Widget + golden-screenshot tests for OnboardingWizard, driven against
// the real wallet-core FFI bridge (not a mock) - see test_helpers.dart.
// Golden PNGs under test/goldens/ are real rendered screenshots of this
// app's UI, viewable directly (no browser/emulator needed) - Flutter's
// standard headless visual-verification mechanism.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/store/wallet_state.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/pages/onboarding/onboarding_wizard.dart';

import '../test_helpers.dart';

Widget _wrap(AppServices services) {
  return MultiProvider(
    providers: [
      Provider<AppServices>.value(value: services),
      ChangeNotifierProvider<WalletState>(create: (_) => WalletState()),
    ],
    child: MaterialApp(theme: buildAppTheme(), home: const OnboardingWizard()),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('shows the choose-signer step first', (tester) async {
    final services = await buildTestServices();
    await tester.pumpWidget(_wrap(services));
    await tester.pumpAndSettle();

    expect(find.text('Get started'), findsOneWidget);
    expect(find.text('Create a new wallet'), findsOneWidget);
    expect(find.text('Import an existing wallet'), findsOneWidget);

    await expectLater(find.byType(OnboardingWizard), matchesGoldenFile('../goldens/onboarding_choose_signer.png'));
  });

  testWidgets('generating a new wallet reveals a real 12-word mnemonic', (tester) async {
    final services = await buildTestServices();
    await tester.pumpWidget(_wrap(services));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Create a new wallet'));
    await tester.pumpAndSettle();

    expect(find.text('Your recovery phrase'), findsOneWidget);
    // 12 numbered words rendered from a real generate_mnemonic() FFI call.
    // No golden-image assertion here: generate_mnemonic() returns a fresh
    // random phrase every run, so the rendered text (and every pixel)
    // legitimately differs run to run - a fixed golden would be flaky by
    // construction. Structural checks above are the right level of proof.
    expect(find.textContaining(RegExp(r'^1\. ')), findsOneWidget);
    expect(find.textContaining(RegExp(r'^12\. ')), findsOneWidget);
  });
}
