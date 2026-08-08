import 'package:flutter_test/flutter_test.dart';
import 'package:sample_app/screens/home_screen.dart';
import 'package:sample_app/data/api_client.dart';

// Contributes NO symbols and no module — only outbound references, so ApiClient and
// HomeScreen do not read as dead while the test itself never becomes a candidate.
// The harness vocabulary (testWidgets, expect, find) is dropped INCLUDING the bare
// names, so `find` cannot vouch for a production method called `find`.
void main() {
  testWidgets('renders', (tester) async {
    await tester.pumpWidget(const HomeScreen());
    expect(find.text('hello'), findsOneWidget);
  });

  test('client', () {
    final c = ApiClient(anyClient);
    c.fetchUser('1');
  });
}
