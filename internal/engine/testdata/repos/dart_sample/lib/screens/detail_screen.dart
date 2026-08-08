import 'package:flutter/material.dart';
import '../data/api_client.dart';

// Constructor injection: ApiClient is a type this repo declares, so it becomes an
// `injects` edge. BuildContext and Key do not — they are framework builtins.
class DetailScreen extends StatelessWidget {
  final ApiClient api;
  const DetailScreen({super.key, required this.api});

  @override
  Widget build(BuildContext context) => const Placeholder();
}
