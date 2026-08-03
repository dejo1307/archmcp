// Package binders holds the registry for plugin.Binder implementations — the passes
// that resolve references across an assembled fact set, once every extractor has run
// and everything is in one store.
//
// Each binder lives in its own subpackage so a new one is an added package and a
// registration line, not an edit to the engine. Before this existed the four of them
// were methods on *Engine, called in a fixed five-call sequence, with the ordering
// constraints between them recorded nowhere: a technology-specific pass could only be
// added by editing the engine, and the one ordering dependency that actually mattered
// looked exactly like the three that did not.
package binders

import "github.com/enola-labs/enola/pkg/plugin"

// Binder is the plugin interface implemented by everything registered here.
type Binder = plugin.Binder

// Registry holds registered binders, in registration order.
type Registry struct {
	binders []Binder
}

// NewRegistry creates an empty binder registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a binder to the registry.
func (r *Registry) Register(b Binder) {
	r.binders = append(r.binders, b)
}

// Get returns the binder with the given name, or nil if not registered.
func (r *Registry) Get(name string) Binder {
	for _, b := range r.binders {
		if b.Name() == name {
			return b
		}
	}
	return nil
}

// All returns every registered binder, in registration order.
func (r *Registry) All() []Binder {
	return r.binders
}

// Stage returns the registered binders declaring the given stage, in registration
// order.
//
// Order WITHIN a stage carries no meaning and must not: the Binder contract requires
// each one to be independent of whether its stage-mates have run. The slice is ordered
// only so that logs and any error report read the same way twice.
func (r *Registry) Stage(s plugin.BindStage) []Binder {
	var out []Binder
	for _, b := range r.binders {
		if b.Stage() == s {
			out = append(out, b)
		}
	}
	return out
}
