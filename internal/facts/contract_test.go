package facts

import "testing"

func TestMessagingProtocolFamily(t *testing.T) {
	tests := map[string]string{
		"kafka": "kafka", "KAFKA": "kafka", " kafka-secure ": "kafka",
		"mqtt": "mqtt", " WSS ": "wss", "": "",
	}
	for protocol, want := range tests {
		if got := MessagingProtocolFamily(protocol); got != want {
			t.Errorf("MessagingProtocolFamily(%q) = %q, want %q", protocol, got, want)
		}
	}
}

func TestIsKafkaProtocol(t *testing.T) {
	for protocol, want := range map[string]bool{
		"kafka": true, "KAFKA": true, " kafka-secure ": true,
		"mqtt": false, "kafkaish": false, "": false,
	} {
		if got := IsKafkaProtocol(protocol); got != want {
			t.Errorf("IsKafkaProtocol(%q) = %v, want %v", protocol, got, want)
		}
	}
}
