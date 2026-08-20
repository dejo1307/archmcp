#!/usr/bin/env node
// enola ESLint reference provider.
//
// Turns ESLint's JSON results into enola lint facts, one per reported message,
// so a constraints rule can wrap a linter's rule with because-prose and a
// ratchet, and the check's one comment covers both engines. The provider
// reads output, never configures ESLint.
//
// Where the results come from, in order:
//   1. ENOLA_ESLINT_RESULTS=<path>   a results file written by the lint step
//                                    CI already runs (`eslint -f json -o ...`)
//   2. <repo>/tmp/eslint-results.json  the same file at its conventional place
//   3. ENOLA_ESLINT_RUN=1            run `npx eslint -f json .` in every
//                                    package under the repo that lists eslint
//                                    (a second lint run; opt in)
// With none of those it emits nothing and says so on stderr: a missing linter
// is a named skip in the census, never an error.
//
// Each fact: kind "lint", name "eslint: <rule> <file>:<line>:<col>", file
// relative to the repository root, line, and props lint_engine, lint_rule,
// lint_severity ("warn" or "error"), message, resolution_level
// "tool-reported" (the provider resolved nothing; this is what the tool said).
// Output is sorted by name so two runs over the same results are
// byte-identical. --version prints a fixed semver and exits.

import { readFileSync, existsSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { join, relative, resolve, dirname } from "node:path";

const PROVIDER_VERSION = "0.1.0";

if (process.argv.includes("--version")) {
  process.stdout.write(PROVIDER_VERSION + "\n");
  process.exit(0);
}

const repo = resolve(process.argv[2] || ".");

function loadResults() {
  const fromEnv = process.env.ENOLA_ESLINT_RESULTS;
  if (fromEnv && existsSync(fromEnv)) {
    return { results: JSON.parse(readFileSync(fromEnv, "utf8")), base: repo, from: fromEnv };
  }
  const conventional = join(repo, "tmp", "eslint-results.json");
  if (existsSync(conventional)) {
    return { results: JSON.parse(readFileSync(conventional, "utf8")), base: repo, from: conventional };
  }
  if (process.env.ENOLA_ESLINT_RUN === "1") {
    const packages = findEslintPackages(repo);
    const results = [];
    for (const dir of packages) {
      let out;
      try {
        out = execFileSync("npx", ["eslint", "--format", "json", "."], { cwd: dir, encoding: "utf8", maxBuffer: 1 << 30, stdio: ["ignore", "pipe", "ignore"] });
      } catch (err) {
        out = err.stdout || "";
      }
      if (out.trim()) {
        results.push(...JSON.parse(out));
      }
    }
    return { results, base: repo, from: `eslint run in ${packages.length} package(s)` };
  }
  return null;
}

function findEslintPackages(root) {
  const out = [];
  const candidates = [root, ...readDirs(root)];
  for (const dir of candidates) {
    const pkg = join(dir, "package.json");
    if (!existsSync(pkg)) continue;
    const text = readFileSync(pkg, "utf8");
    if (/"eslint"\s*:/.test(text) && existsSync(join(dir, "node_modules", ".bin", "eslint"))) {
      out.push(dir);
    }
  }
  return out;
}

function readDirs(root) {
  try {
    return readdirSync(root, { withFileTypes: true })
      .filter((d) => d.isDirectory() && !["node_modules", ".git", "vendor", "tmp"].includes(d.name))
      .map((d) => join(root, d.name));
  } catch {
    return [];
  }
}
import { readdirSync } from "node:fs";

const loaded = loadResults();
if (!loaded) {
  process.stderr.write("enola eslint provider: no results (set ENOLA_ESLINT_RESULTS, write tmp/eslint-results.json, or ENOLA_ESLINT_RUN=1); emitting nothing\n");
  process.exit(0);
}

// Identity is rule and file, with an ordinal for the second and later
// findings of the same rule in the same file, in line order. A line number in
// the name would make every edit above a warning read as a new finding to a
// ratchet; with the ordinal, fixing one warning in a file retires the last
// ordinal and adding one mints the next, and line shifts change nothing.
// Line and column stay on the fact as data.
const perRuleFile = new Map();
for (const fileResult of loaded.results) {
  const abs = resolve(fileResult.filePath);
  let file = relative(loaded.base, abs);
  if (file.startsWith("..")) file = fileResult.filePath;
  for (const m of fileResult.messages || []) {
    if (!m.ruleId) continue;
    const key = `${m.ruleId}\u0000${file}`;
    if (!perRuleFile.has(key)) perRuleFile.set(key, []);
    perRuleFile.get(key).push({ file, rule: m.ruleId, line: m.line || 0, column: m.column || 0, severity: m.severity, message: m.message });
  }
}
const lines = [];
for (const group of perRuleFile.values()) {
  group.sort((a, b) => a.line - b.line || a.column - b.column);
  group.forEach((m, i) => {
    lines.push(JSON.stringify({
      kind: "lint",
      name: i === 0 ? `eslint: ${m.rule} ${m.file}` : `eslint: ${m.rule} ${m.file} #${i + 1}`,
      file: m.file,
      line: m.line,
      props: {
        lint_engine: "eslint",
        lint_rule: m.rule,
        lint_severity: m.severity === 2 ? "error" : "warn",
        column: m.column,
        message: String(m.message || "").slice(0, 200),
        resolution_level: "tool-reported",
      },
    }));
  });
}
lines.sort();
process.stdout.write(lines.join("\n") + (lines.length ? "\n" : ""));
process.stderr.write(`enola eslint provider: ${lines.length} finding(s) from ${loaded.from}\n`);
