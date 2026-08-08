import 'package:http/http.dart' as http;

// A Flutter client's architectural value is almost entirely in what it CALLS: it
// serves nothing, imports nothing from the backend and shares no code with it, so
// these call sites are the only structural evidence the two belong to one system.
class OrdersApi {
  final http.Client client;
  OrdersApi(this.client);

  Future<void> listOrders() async {
    await client.get(Uri.parse('/api/orders'));
  }

  Future<void> getOrder(String id) async {
    await client.get(Uri.parse('/api/orders/detail'));
  }

  Future<void> createOrder() async {
    await client.post(Uri.parse('/api/orders'));
  }
}
