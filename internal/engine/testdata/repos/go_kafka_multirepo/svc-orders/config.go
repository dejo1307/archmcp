package main

// The svc-orders service owns the "svc-orders.*" topic namespace. It declares only
// ONE of the two topics the billing consumer subscribes to — svc-orders.audit_logged
// is published by code this fixture deliberately does not contain, which is what
// pins the linker's "resolves from the consumer side alone" property: the edge must
// still carry that topic even though no producer-side fact exists for it.
type Config struct {
	OrderPlacedTopic string `envconfig:"ORDER_PLACED_TOPIC" default:"svc-orders.order_placed"`
}

// A repo referencing a topic it OWNS is intra-repo: the topic fact is emitted, but
// the linker draws no cross-repo edge from svc-orders back to itself.
func main() {}
