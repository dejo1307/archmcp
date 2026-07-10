import { formatTags } from "./util";

// A co-located unit test is this helper's only caller. The engine collects it via
// config.TestGlobs and tsextractor.ExtractTestRefs emits a test_ref edge to
// src.formatTags, so it is not mis-reported as dead code.
test("formatTags trims and joins", () => {
  expect(formatTags([" a ", "b "])).toBe("a,b");
});
