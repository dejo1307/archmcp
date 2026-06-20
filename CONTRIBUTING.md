# Contributing to enola

Thank you for your interest in contributing to enola. Every contribution — code, documentation, bug reports, feature ideas — helps make the project better for everyone.

## Getting started

1. **Fork and clone** the repository.
2. Make sure you have **Go 1.25+** and a **C compiler** (for tree-sitter bindings).
3. Build and verify:

   ```bash
   go build -o enola ./cmd/enola
   go test ./...
   ```

4. Create a branch for your work:

   ```bash
   git checkout -b my-feature
   ```

## What to work on

- **Bug reports and fixes** — if something doesn't work, open an issue or submit a fix.
- **New language extractors** — enola's architecture makes it straightforward to add support for additional languages. See [`ARCHITECTURE.md`](ARCHITECTURE.md) for how extractors, explainers, and renderers fit together.
- **Improved detection and extraction** — better framework support, more accurate dependency resolution, richer symbol extraction.
- **Documentation** — typo fixes, clearer explanations, new examples.

If you're considering a larger change, please open an issue first so we can discuss the approach before you invest significant time.

## Submitting changes

1. Keep commits focused. One logical change per commit.
2. Write clear commit messages that explain *why*, not just *what*.
3. Add or update tests for any code changes.
4. Make sure all tests pass before submitting:

   ```bash
   go test ./...
   ```

5. Open a pull request against `main`. Describe what the change does and why.

## Code style

- Follow standard Go conventions (`gofmt`, `go vet`).
- Prefer clear code over clever code.
- Default to no comments — add one only when the *why* is non-obvious.

## Reporting bugs

Open a [GitHub issue](https://github.com/enola-labs/enola/issues) with:

- What you did (command, config, repository structure).
- What you expected.
- What happened instead.
- enola version and OS.

## Community standards

All participants are expected to follow our [Code of Conduct](CODE_OF_CONDUCT.md). We are committed to making this a welcoming and respectful community for everyone.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
