part of 'user.dart';

// Declares no imports; it shares user.dart's. The extractor re-walks this file with
// the host's import set so any framework gate still applies here.
String displayName(User u) => u.email.split('@').first;
