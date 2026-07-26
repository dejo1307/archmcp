// The frontend calls its OWN backend, which lives in this same repo. The
// acme-rs repo serves an API-compatible surface, so without the intra-repo
// preference these calls bind to acme-rs and fabricate an acme -> acme-rs edge.
export async function fetchResults() {
  const res = await fetch("/api/v1/search/results");
  return res.json();
}
