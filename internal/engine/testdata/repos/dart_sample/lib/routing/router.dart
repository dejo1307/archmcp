import 'package:go_router/go_router.dart';
import '../screens/home_screen.dart';
import '../screens/detail_screen.dart';

// Three shapes in one config:
//   - a literal path,
//   - a path declared as a constant on the screen (resolved repo-wide),
//   - a nested sub-route, whose relative path composes onto its parent.
final router = GoRouter(
  routes: [
    GoRoute(
      path: '/',
      builder: (context, state) => const HomeScreen(),
      routes: [
        GoRoute(
          path: 'detail/:id',
          builder: (context, state) => const DetailScreen(),
        ),
      ],
    ),
    GoRoute(
      path: SettingsScreen.routeName,
      builder: (context, state) => const SettingsScreen(),
    ),
  ],
);
