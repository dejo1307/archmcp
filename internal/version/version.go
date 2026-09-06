// Package version holds the enola build version as a single neutral leaf
// package, so any package (server, engine, …) can read it without creating an
// import cycle. It is set at build time via -ldflags:
//
//	-X github.com/enola-labs/enola/internal/version.Version=<tag>
package version

// Version is the enola build version. It defaults to "dev" for local builds and
// is overwritten at release time via the linker flag above.
var Version = "dev"

// InstallMethod records who owns the installed binary, so a command that would
// modify the installation can refuse when the answer is "not enola". It is set
// the same way as Version:
//
//	-X github.com/enola-labs/enola/internal/version.InstallMethod=pip
//
// "source" is the default and covers a local `go build` as well as the release
// tarball, both of which leave the file to whoever put it there. "pip" is set by
// the PyPI wheel built in packaging/pypi, where pip records the file's hash and
// path and will overwrite or remove it on the next operation.
//
// Only "pip" currently changes any behaviour. The value is deliberately a plain
// string rather than a bool so that a future installer (a distro package, a
// Homebrew formula) can name itself without another linker flag.
var InstallMethod = "source"
