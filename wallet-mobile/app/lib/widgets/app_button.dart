// Mirrors wallet-web's components/Button.tsx - primary-800 filled button.
import 'package:flutter/material.dart';

class AppButton extends StatelessWidget {
  const AppButton({super.key, required this.label, this.onPressed, this.enabled = true});

  final String label;
  final VoidCallback? onPressed;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return ElevatedButton(
      onPressed: enabled ? onPressed : null,
      child: Text(label),
    );
  }
}
