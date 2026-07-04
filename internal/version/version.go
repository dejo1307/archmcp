// Package version holds the enola build version as a single neutral leaf
// package, so any package (server, engine, …) can read it without creating an
// import cycle. It is set at build time via -ldflags:
//
//	-X github.com/enola-labs/enola/internal/version.Version=<tag>
package version

// Version is the enola build version. It defaults to "dev" for local builds and
// is overwritten at release time via the linker flag above.
var Version = "dev"
