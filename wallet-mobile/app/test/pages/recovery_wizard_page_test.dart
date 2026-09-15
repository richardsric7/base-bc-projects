// Widget test for RecoveryWizardPage's first (username) step.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:wallet_mobile/app_services.dart';
import 'package:wallet_mobile/store/wallet_state.dart';
import 'package:wallet_mobile/theme/app_theme.dart';
import 'package:wallet_mobile/pages/recovery/recovery_wizard_page.dart';

import '../test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('renders the username step first', (tester) async {
    final services = await buildTestServices();
    final wallet = WalletState();

    await tester.pumpWidget(
      MultiProvider(
        providers: [
          Provider<AppServices>.value(value: services),
          ChangeNotifierProvider<WalletState>.value(value: wallet),
        ],
        child: MaterialApp(
          theme: buildAppTheme(),
          initialRoute: '/',
          routes: {'/': (_) => const RecoveryWizardPage(), '/unlock': (_) => const Scaffold(body: Text('unlock'))},
        ),
      ),
    );
    await tester.pump();

    expect(find.text('Account recovery'), findsOneWidget);
    expect(find.text("What's your username?"), findsOneWidget);
    expect(find.text('Username'), findsOneWidget);
    expect(find.text('Back to unlock'), findsOneWidget);
  });
}
