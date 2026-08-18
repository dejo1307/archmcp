package orders

import "github.com/segmentio/kafka-go"

type Publisher interface {
	Publish(topic string, payload any)
}

func publishOrder(publisher Publisher, payload any) {
	publisher.Publish("svc-orders.order_created", payload)
}

var _ kafka.Message
