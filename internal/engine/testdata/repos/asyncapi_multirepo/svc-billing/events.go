package billing

import "github.com/segmentio/kafka-go"

type Consumer interface {
	Subscribe(topic string, handler func())
}

func subscribeOrders(consumer Consumer, handler func()) {
	consumer.Subscribe("svc-orders.order_created", handler)
}

var _ kafka.Message
