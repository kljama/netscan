package monitoring

import (
	"testing"

	"github.com/kljama/netscan/internal/state"
)

func BenchmarkPerformPing_Validation(b *testing.B) {
	device := state.Device{IP: "192.168.1.1"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validateIPAddress(device.IP)
	}
}
