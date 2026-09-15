// App entry point - the mobile counterpart to wallet-web's App.tsx +
// main.tsx. Hydrates known vault state at startup (mirroring App.tsx's
// useVaultHydration: the primary wallet's address comes from the offline
// cache first, falling back to a live GET /v1/users/me only if
// uncached), then picks the correct starting screen.
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'app_services.dart';
import 'store/wallet_state.dart';
import 'store/offline_cache.dart';
import 'theme/app_theme.dart';
import 'pages/onboarding/onboarding_wizard.dart';
import 'pages/unlock/unlock_page.dart';
import 'pages/dashboard/dashboard_page.dart';
import 'pages/send/send_page.dart';
import 'pages/settings/settings_page.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final services = await AppServices.create();
  final wallet = WalletState();
  await _hydrate(services, wallet);

  runApp(
    MultiProvider(
      providers: [
        Provider<AppServices>.value(value: services),
        ChangeNotifierProvider<WalletState>.value(value: wallet),
      ],
      child: const TrovoWalletApp(),
    ),
  );
}

/// Resolves the primary wallet's address from the offline cache first
/// (works with no network); falls back to a live GET /v1/users/me only
/// if nothing is cached yet (e.g. cache was cleared) - see wallet-web's
/// App.tsx resolvePrimaryWallet for the identical reasoning.
Future<void> _hydrate(AppServices services, WalletState wallet) async {
  final hasSignerVault = await services.walletCore.hasVault('signer');
  final signerAddress = await services.walletCore.getKnownAddress('signer');

  String? primaryAddress;
  if (signerAddress != null) {
    final cached = services.cache.getEntry(primaryWalletAddressKey(signerAddress));
    if (cached != null) {
      primaryAddress = cached.data;
    } else {
      try {
        final user = await services.users.getMyUser(signerAddress);
        await services.cache.setEntry(primaryWalletAddressKey(signerAddress), user.address);
        primaryAddress = user.address;
      } catch (_) {
        primaryAddress = null;
      }
    }
  }

  wallet.hydrate(
    signer: RoleState(address: signerAddress, hasVault: hasSignerVault, isUnlocked: false),
    // No unlock step exists (or is needed) for a Safe with no key of its
    // own - knowing its address makes it "unlocked" too, unlike signer.
    primary: RoleState(address: primaryAddress, hasVault: primaryAddress != null, isUnlocked: primaryAddress != null),
  );
}

class TrovoWalletApp extends StatelessWidget {
  const TrovoWalletApp({super.key});

  @override
  Widget build(BuildContext context) {
    final wallet = context.watch<WalletState>();
    final onboardingComplete = wallet.onboardingComplete;
    final isFullyUnlocked = wallet.isFullyUnlocked;

    final String initialRoute;
    if (!onboardingComplete) {
      initialRoute = '/onboarding';
    } else if (!isFullyUnlocked) {
      initialRoute = '/unlock';
    } else {
      initialRoute = '/dashboard';
    }

    return MaterialApp(
      title: 'Trovo Wallet',
      theme: buildAppTheme(),
      initialRoute: initialRoute,
      routes: {
        '/onboarding': (_) => const OnboardingWizard(),
        '/unlock': (_) => const UnlockPage(),
        '/dashboard': (_) => const DashboardPage(),
        '/send': (_) => const SendPage(),
        '/settings': (_) => const SettingsPage(),
      },
    );
  }
}
