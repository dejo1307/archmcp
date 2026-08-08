import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/user.dart';

// Outbound call sites. The relative paths stay internal and therefore linkable to a
// backend loaded in the same snapshot; the absolute one is tagged external with its
// host, so it is not counted as an unresolved internal edge.
class ApiClient {
  final http.Client httpClient;

  ApiClient(this.httpClient);

  Future<User> fetchUser(String id) async {
    final res = await httpClient.get(Uri.parse('/api/users/$id'));
    return User(id: id, email: jsonDecode(res.body)['email'] as String, token: '');
  }

  Future<void> createOrder(Json body) async {
    await httpClient.post(Uri.parse('/api/orders'));
  }

  Future<void> reportCrash() async {
    await httpClient.post(Uri.parse('https://crash.example.com/v1/report'));
  }

  // A per-iteration network call behind no wrapper: loop_depth 1 with an io_direct
  // body, which is the shape the performance analyzer reads as an N+1.
  Future<void> syncAll(List<String> ids) async {
    for (final id in ids) {
      await fetchUser(id);
    }
  }
}
