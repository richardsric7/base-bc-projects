// Mirrors wallet-web's components/TextInput.tsx.
import 'package:flutter/material.dart';

class AppTextInput extends StatefulWidget {
  const AppTextInput({
    super.key,
    required this.label,
    required this.value,
    required this.onChanged,
    this.obscureText = false,
    this.keyboardType,
  });

  final String label;
  final String value;
  final ValueChanged<String> onChanged;
  final bool obscureText;
  final TextInputType? keyboardType;

  @override
  State<AppTextInput> createState() => _AppTextInputState();
}

class _AppTextInputState extends State<AppTextInput> {
  late final TextEditingController _controller = TextEditingController(text: widget.value);
  bool _showPlainText = false;

  @override
  Widget build(BuildContext context) {
    final isPassword = widget.obscureText;
    return TextField(
      controller: _controller,
      obscureText: isPassword && !_showPlainText,
      keyboardType: widget.keyboardType,
      onChanged: widget.onChanged,
      decoration: InputDecoration(
        labelText: widget.label,
        suffixIcon: isPassword
            ? TextButton(
                onPressed: () => setState(() => _showPlainText = !_showPlainText),
                child: Text(_showPlainText ? 'Hide' : 'Show'),
              )
            : null,
      ),
    );
  }
}
