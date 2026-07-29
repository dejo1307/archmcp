// The frontend calls its OWN backend, which lives in this same repo. The
// acme-rs repo serves an API-compatible surface, so without the intra-repo
// preference these calls bind to acme-rs and fabricate an acme -> acme-rs edge.
export async function fetchResults() {
  const res = await fetch("/api/v1/search/results");
  return res.json();
}

// v141 regression fixture. Two things here were invisible before that version, and
// each alone was enough to erase the call: the verb is LOWERCASE (only uppercase
// generated-client verbs were matched, so the dominant axios idiom produced no
// fact), and the type argument is NESTED (the "<[^>]*>" group stopped at the inner
// ">", which also broke the uppercase and fetch paths it was written for). The
// failure was silent — the call was not counted detected, external or unresolved,
// so a cross-repo residual under-reported with no sign that it had.
export async function submitSearch(body: Query) {
  return axios.post<ApiResponse<Result[]>>("/api/v1/search", body);
}
