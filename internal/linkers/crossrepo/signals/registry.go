// Package signals holds the registry for plugin.CrossRepoSignal implementations — the
// independent pieces of evidence that one repository depends on another.
//
// Each signal lives in its own subpackage so a new one is an added package and a
// registration line. Before this existed, all four lived in one 1,800-line file: each
// had a dedicated field on a shared evidence struct and a dedicated block in a shared
// materializer, so a fifth signal meant editing both, and out-of-tree code could add
// none at all.
package signals

import "github.com/enola-labs/enola/pkg/plugin"

// Signal is the plugin interface implemented by everything registered here.
type Signal = plugin.CrossRepoSignal

// Registry holds registered cross-repo signals, in registration order.
type Registry struct {
	signals []Signal
}

// NewRegistry creates an empty signal registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a signal to the registry.
func (r *Registry) Register(s Signal) {
	r.signals = append(r.signals, s)
}

// Get returns the signal with the given name, or nil if not registered.
func (r *Registry) Get(name string) Signal {
	for _, s := range r.signals {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// All returns every registered signal, in registration order.
//
// The order is presentation only. ComputeLinks partitions by declared phase and runs
// directional signals before symmetric ones; within a phase, signals never observe each
// other, so the sequence cannot affect the result.
func (r *Registry) All() []Signal {
	return r.signals
}
