import 'package:flutter/material.dart';
import '../core/base.dart';

// `static const routeName` is how a Flutter screen declares its own path; the router
// refers to it rather than repeating the literal.
class HomeScreen extends StatelessWidget with Timestamped {
  static const routeName = '/home';

  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Home')),
      body: const Text('hello'),
    );
  }
}

class SettingsScreen extends StatefulWidget {
  static const routeName = '/settings';
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  @override
  Widget build(BuildContext context) => const Placeholder();
}
