// Exercises the four type constructs plus a typedef, and the abstractness rule:
// AuthGuard is abstract by keyword, Loggable is a mixin whose members ALL lack a
// body (so it is an abstraction), Timestamped is a mixin that carries an
// implementation (so it is not).
typedef Json = Map<String, dynamic>;

abstract class AuthGuard {
  bool canEnter(String route);
}

mixin Loggable {
  void log(String message);
}

mixin Timestamped {
  DateTime get now => DateTime.now();
  String stamp() => now.toIso8601String();
}

extension StringCasing on String {
  String get titled => isEmpty ? this : this[0].toUpperCase() + substring(1);
}

enum Role { admin, member, guest }
