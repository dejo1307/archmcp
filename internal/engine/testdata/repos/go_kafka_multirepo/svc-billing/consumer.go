package main

import "example.com/svc-billing/internal/env"

// Config's *Topic fields carry their topic name in the `default:` tag — the value
// the service uses unless the environment overrides it.
type Config struct {
	// Owned by svc-orders (leading segment before the first "."), which IS a loaded
	// repo: draws the cross-repo edge svc-billing -> svc-orders.
	OrderPlacedTopic string `envconfig:"ORDER_PLACED_TOPIC" default:"svc-orders.order_placed"`

	// Owned by this repo: intra-repo, so no edge — svc-billing is the producer here.
	InvoiceRetryTopic string `envconfig:"INVOICE_RETRY_TOPIC" default:"svc-billing.invoice_retry"`
}

func subscribe(bus *Bus) {
	// env.Get's second argument is the default topic. svc-orders declares no fact for
	// this one, so the edge has to resolve from the consumer side alone.
	audit := env.Get("ORDERS_AUDIT_TOPIC", "svc-orders.audit_logged")

	// Owned by no loaded repo (an export sink): left unlinked rather than guessed at.
	export := env.Get("EXPORT_TOPIC", "analytics-sink.rows_exported")

	_, _ = audit, export

	// An in-process event bus names a Go SYMBOL, not a topic string, so nothing here
	// anchors on a topic marker and no topic fact is emitted — by construction, not
	// by a filter that could drift.
	bus.Subscribe(InvoicePaid{}, onInvoicePaid)
}

type Bus struct{}

func (b *Bus) Subscribe(event any, handler func()) {}

type InvoicePaid struct{}

func onInvoicePaid() {}
