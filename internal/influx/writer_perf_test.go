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
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = w.WritePingResult(ip, rtt, success)
	}
}

// BenchmarkWriteDeviceInfo measures the performance of WriteDeviceInfo
func BenchmarkWriteDeviceInfo(b *testing.B) {
	mockAPI := &MockAsyncWriteAPI{
		errChan: make(chan error, 10),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	batchChan := make(chan *write.Point, b.N+100)

	w := &Writer{
		writeAPI:         mockAPI,
		primaryErrorChan: mockAPI.errChan,
		batchChan:        batchChan,
		ctx:              ctx,
	}

	go func() {
		for {
			select {
			case <-batchChan:
			case <-ctx.Done():
				return
			}
		}
	}()

	ip := "192.168.1.1"
	hostname := "router.local"
	sysDescr := "Linux Router 5.10"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = w.WriteDeviceInfo(ip, hostname, sysDescr)
	}
}

// BenchmarkWriteHealthMetrics measures the performance of WriteHealthMetrics
func BenchmarkWriteHealthMetrics(b *testing.B) {
	mockAPI := &MockAsyncWriteAPI{
		errChan: make(chan error, 10),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &Writer{
		healthWriteAPI: mockAPI,
		ctx:            ctx,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.WriteHealthMetrics(100, 50, 200, 64, 128, 10, true, 5000, 0, 0, 5000)
	}
}
