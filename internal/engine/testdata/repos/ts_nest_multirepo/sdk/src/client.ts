import axios from "axios";

// The consumer half. These are v141's lowercase-verb client calls, and their paths
// are exactly what the api repo's decorators compose to — so this fixture pins the
// whole chain in one golden: decorators become server routes (v142), lowercase axios
// calls become client routes (v141), and the cross-repo linker resolves the two into
// a real dependency edge. Before v142 the server side did not exist, so every one of
// these was an unresolved edge and the api repo was classified `isolated`.
export async function getAvailableSlots() {
  return axios.get<ApiResponse<Slot[]>>("/v2/slots/available");
}

export async function reserveSlot(body: ReserveBody) {
  return axios.post<ApiResponse<string>>("/v2/slots/reserve", body);
}

export async function listUsers() {
  return axios.get("/users");
}

export async function cancelOrder(id: string) {
  return axios.post(`/api/orders/${id}/cancel`, {});
}
