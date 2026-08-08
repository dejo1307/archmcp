import 'package:flutter/material.dart';
import 'routing/router.dart';
import 'screens/home_screen.dart';

void main() {
  runApp(const SampleApp());
}

class SampleApp extends StatelessWidget {
  const SampleApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      routes: {
        '/legacy': (context) => const HomeScreen(),
      },
    );
  }
}
