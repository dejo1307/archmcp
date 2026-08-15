package telemetry

import "fmt"

// Record is deliberately outside the layer order — nothing declares it, and nothing
// depends on it. It exists so a change can touch a package the author never mentioned.
func Record(event string) {
	fmt.Println(event)
}
