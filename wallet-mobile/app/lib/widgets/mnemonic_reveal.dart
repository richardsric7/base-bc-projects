// Mirrors wallet-web's pages/onboarding/MnemonicReveal.tsx - shows a
// freshly generated mnemonic exactly once, then requires the user to
// re-type it before continuing, a real check that they copied it down
// correctly rather than a "yes I saved it" checkbox.
import 'package:flutter/material.dart';
import 'app_button.dart';
import 'app_button_secondary.dart';

class MnemonicReveal extends StatefulWidget {
  const MnemonicReveal({super.key, required this.mnemonic, required this.onConfirmed});

  final String mnemonic;
  final VoidCallback onConfirmed;

  @override
  State<MnemonicReveal> createState() => _MnemonicRevealState();
}

class _MnemonicRevealState extends State<MnemonicReveal> {
  bool _confirming = false;
  final _controller = TextEditingController();
  String _error = '';

  String _normalize(String s) => s.trim().toLowerCase().replaceAll(RegExp(r'\s+'), ' ');

  void _confirm() {
    if (_normalize(_controller.text) != _normalize(widget.mnemonic)) {
      setState(() => _error = 'That does not match the phrase shown - please check and try again.');
      return;
    }
    widget.onConfirmed();
  }

  @override
  Widget build(BuildContext context) {
    final words = widget.mnemonic.trim().split(RegExp(r'\s+'));

    if (!_confirming) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text('Your recovery phrase', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Text(
            'Write down these ${words.length} words in order and store them somewhere safe. Anyone with this '
            'phrase can control this wallet - this app will never ask for it again after this screen.',
            style: Theme.of(context).textTheme.bodyMedium,
          ),
          const SizedBox(height: 16),
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(color: const Color(0xFFF2F6F9), borderRadius: BorderRadius.circular(8)),
            child: Wrap(
              spacing: 12,
              runSpacing: 8,
              children: [
                for (var i = 0; i < words.length; i++)
                  SizedBox(width: 100, child: Text('${i + 1}. ${words[i]}', style: const TextStyle(fontFamily: 'monospace'))),
              ],
            ),
          ),
          const SizedBox(height: 16),
          AppButton(label: "I've saved my recovery phrase", onPressed: () => setState(() => _confirming = true)),
        ],
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Confirm your recovery phrase', style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        Text("Re-type the phrase you just saved to confirm it's correct.", style: Theme.of(context).textTheme.bodyMedium),
        const SizedBox(height: 16),
        TextField(controller: _controller, maxLines: 3, decoration: const InputDecoration(border: OutlineInputBorder())),
        if (_error.isNotEmpty) ...[
          const SizedBox(height: 8),
          Text(_error, style: const TextStyle(color: Colors.red)),
        ],
        const SizedBox(height: 16),
        AppButton(label: 'Confirm', onPressed: _confirm),
        const SizedBox(height: 8),
        AppButtonSecondary(label: 'Back', onPressed: () => setState(() => _confirming = false)),
      ],
    );
  }
}
