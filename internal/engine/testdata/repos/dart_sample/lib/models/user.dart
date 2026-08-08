// A data holder: public final fields and no behaviour. `part` pulls in a file that
// declares no imports of its own — the part inherits this library's import scope.
part 'user_helpers.dart';

class User {
  final String id;
  final String email;
  final String _token;

  const User({required this.id, required this.email, required String token})
      : _token = token;
}
