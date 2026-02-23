package influx

import (
	"context"
	"testing"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
)

// BenchmarkWritePingResult measures the performance of WritePingResult
func BenchmarkWritePingResult(b *testing.B) {
	// Setup
	mockAPI := &MockAsyncWriteAPI{
		errChan: make(chan error, 10),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a large buffered channel to minimize channel overhead impact
	// In a real scenario, this would be drained by backgroundFlusher
	batchChan := make(chan *write.Point, b.N+100)

	w := &Writer{
		writeAPI:         mockAPI,
		primaryErrorChan: mockAPI.errChan,
		batchChan:        batchChan,
		ctx:              ctx,
	}

	// Start a goroutine to drain the channel if it gets full (fallback)
	// although we sized it to b.N so it shouldn't block
	go func() {
		for {
			select {
			case <-batchChan:
				// discard
			case <-ctx.Done():
				return
			}
		}
	}()

	ip := "192.168.1.1"
	rtt := 10 * time.Millisecond
	success := true

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.WritePingResult(ip, rtt, success)
	}
}
