package influx

import (
	"testing"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
    influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

// MockAsyncWriteAPI implements api.WriteAPI for testing
type MockAsyncWriteAPI struct {
	errChan chan error
}

func (m *MockAsyncWriteAPI) WritePoint(point *write.Point) {}
func (m *MockAsyncWriteAPI) WriteRecord(line string)       {}
func (m *MockAsyncWriteAPI) Errors() <-chan error          { return m.errChan }
func (m *MockAsyncWriteAPI) Flush()                        {}
func (m *MockAsyncWriteAPI) Close()                        {}
func (m *MockAsyncWriteAPI) SetWriteFailedCallback(cb api.WriteFailedCallback) {}

// BenchmarkFlushBatch measures the performance of flushing a batch
func BenchmarkFlushBatch(b *testing.B) {
	// Setup
	mockAPI := &MockAsyncWriteAPI{
		errChan: make(chan error, 10),
	}

    // Create a Writer with the mock API manually to avoid NewWriter logic
    w := &Writer{
        writeAPI:         mockAPI,
        primaryErrorChan: mockAPI.errChan,
        batchSize:        100,
        // We don't need other fields for this benchmark
    }

	// Create a dummy point
	p := influxdb2.NewPoint(
		"test",
		map[string]string{"tag": "value"},
		map[string]interface{}{"field": 1},
		time.Now(),
	)

    points := []*write.Point{p}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.flushBatch(points)
	}
}
