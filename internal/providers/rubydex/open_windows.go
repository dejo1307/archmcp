//go:build windows

package rubydex

import "errors"

// ErrUnsupportedHost says the engine library cannot be loaded on this host.
var ErrUnsupportedHost = errors.New("the Rubydex provider loads a shared library through dlopen, which this build does not do on Windows")

// Open reports that the provider is unavailable on Windows; a configured
// provider becomes a named skip rather than a failed snapshot.
func Open(path string) (*Library, error) {
	return nil, ErrUnsupportedHost
}
