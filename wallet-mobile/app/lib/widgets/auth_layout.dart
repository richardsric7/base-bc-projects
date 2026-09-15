// Mirrors wallet-web's components/AuthLayout.tsx - a title/subtitle
// header over the form content. wallet-web's split-screen brand panel
// doesn't translate to a phone-width layout, so this collapses to a
// single column (the same collapse wallet-web itself makes below its own
// `md:` breakpoint). The brand mark above the title is the real
// production mobile app's own logo asset (PLAN.md §14.5), not a
// re-drawn substitute.
import 'package:flutter/material.dart';

class AuthLayout extends StatelessWidget {
  const AuthLayout({super.key, required this.title, this.subtitle, required this.child});

  final String title;
  final String? subtitle;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const SizedBox(height: 24),
                Image.asset('assets/images/trovo_app.png', height: 32, semanticLabel: 'Trovo App'),
                const SizedBox(height: 32),
                Text(title, textAlign: TextAlign.center, style: Theme.of(context).textTheme.headlineMedium),
                if (subtitle != null) ...[
                  const SizedBox(height: 8),
                  Text(subtitle!, textAlign: TextAlign.center, style: Theme.of(context).textTheme.bodyMedium),
                ],
                const SizedBox(height: 24),
                child,
              ],
            ),
          ),
        ),
      ),
    );
  }
}
