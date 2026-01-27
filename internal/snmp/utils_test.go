package snmp

import (
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/rs/zerolog"
)

func init() {
	zerolog.SetGlobalLevel(zerolog.Disabled)
}

// MockSNMPClient simulates SNMP calls
type MockSNMPClient struct {
	GetFunc     func(oids []string) (*gosnmp.SnmpPacket, error)
	GetNextFunc func(oids []string) (*gosnmp.SnmpPacket, error)
	Target      string
	Delay       time.Duration
	CallCount   int
}

func (m *MockSNMPClient) Get(oids []string) (*gosnmp.SnmpPacket, error) {
	if m.GetFunc != nil {
		return m.GetFunc(oids)
	}
	// Default: return NoSuchInstance to trigger fallback
	vars := make([]gosnmp.SnmpPDU, len(oids))
	for i := range oids {
		vars[i] = gosnmp.SnmpPDU{Type: gosnmp.NoSuchInstance}
	}
	return &gosnmp.SnmpPacket{Variables: vars}, nil
}

func (m *MockSNMPClient) GetNext(oids []string) (*gosnmp.SnmpPacket, error) {
	m.CallCount++
	time.Sleep(m.Delay)
	if m.GetNextFunc != nil {
		return m.GetNextFunc(oids)
	}
	// Default: return variables that match validation
	vars := make([]gosnmp.SnmpPDU, len(oids))
	for i, oid := range oids {
		vars[i] = gosnmp.SnmpPDU{
			Name:  oid + ".0", // Append .0 to simulate valid child
			Type:  gosnmp.OctetString,
			Value: "mock value",
		}
	}
	return &gosnmp.SnmpPacket{Variables: vars}, nil
}

func (m *MockSNMPClient) GetTarget() string {
	return m.Target
}

func BenchmarkGetWithFallback(b *testing.B) {
	oids := []string{
		"1.3.6.1.2.1.1.1.0", // sysDescr
		"1.3.6.1.2.1.1.5.0", // sysName
		"1.3.6.1.2.1.1.2.0", // sysObjectID
		"1.3.6.1.2.1.1.3.0", // sysUpTime
	}

	mockClient := &MockSNMPClient{
		Delay:  1 * time.Millisecond, // 1ms latency per call
		Target: "127.0.0.1",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GetWithFallback(mockClient, oids)
	}
}

func TestGetWithFallback_Correctness(t *testing.T) {
	oids := []string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0"}
	mockClient := &MockSNMPClient{
		Target: "127.0.0.1",
	}

	pkt, err := GetWithFallback(mockClient, oids)
	if err != nil {
		t.Fatalf("GetWithFallback failed: %v", err)
	}

	if len(pkt.Variables) != 2 {
		t.Errorf("Expected 2 variables, got %d", len(pkt.Variables))
	}

	// Check order and content if possible, but Mock returns generic stuff.
	// We just ensure it works.
}
