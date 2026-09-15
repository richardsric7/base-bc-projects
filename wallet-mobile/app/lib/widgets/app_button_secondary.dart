// Mirrors wallet-web's components/ButtonSecondary.tsx - outlined button.
import 'package:flutter/material.dart';

class AppButtonSecondary extends StatelessWidget {
  const AppButtonSecondary({super.key, required this.label, this.onPressed, this.enabled = true});

  final String label;
  final VoidCallback? onPressed;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton(
      onPressed: enabled ? onPressed : null,
      child: Text(label),
    );
  }
}
