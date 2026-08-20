# ESLint as a fact provider

`enola_eslint_provider.mjs` turns ESLint's JSON results into enola `lint`
facts, one per reported message, through the provider seam. Configure it like
any provider:

```yaml
providers:
  - name: eslint
    command: ["node", "path/to/examples/providers/js/eslint/enola_eslint_provider.mjs"]
    expected_version: "0.1.0"
```

It reads, never lints, unless asked: `ENOLA_ESLINT_RESULTS=<file>` or
`<repo>/tmp/eslint-results.json` (written by `eslint -f json -o ...` in the
lint step CI already runs), or `ENOLA_ESLINT_RUN=1` to run `npx eslint -f json`
in every package that lists eslint. Without any of those it emits nothing and
the census shows the provider as skipped.

A constraint then wraps a linter's rule with because-prose and a ratchet:

```yaml
components:
  - name: mutating-args-warnings
    kind: lint
    match: ["ember_app/**"]
    where:
      lint_rule: "tt/no-mutating-args"
rules:
  - id: no-mutating-args
    forbid_fact: mutating-args-warnings
    mode: ratchet
    because: "Arguments are the caller's state; writing through them hides the write from the owner. ESLint authors the rule and gives in-editor feedback; this gives it a baseline and a place in the one PR comment."
```
