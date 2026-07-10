// formatTags is a production helper exercised only by its co-located test. Before
// the TS test-ref gate (v103) it had no incoming edge and was reported dead; the
// test file below now credits it.
export function formatTags(tags: string[]): string {
  return tags.map((t) => t.trim()).join(",");
}
