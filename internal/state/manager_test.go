package state

import (
	"testing"
	"time"
)

func TestManagerAddGetPrune(t *testing.T) {
	mgr := NewManager(1000) // Test with max 1000 devices
	dev := Device{IP: "1.2.3.4", Hostname: "host", SysDescr: "desc", LastSeen: time.Now()}
	mgr.Add(dev)
	got, ok := mgr.Get("1.2.3.4")
	if !ok || got.IP != "1.2.3.4" {
		t.Errorf("expected device to be added and retrievable")
	}
	all := mgr.GetAll()
	if len(all) != 1 {
		t.Errorf("expected one device, got %d", len(all))
	}
	mgr.UpdateLastSeen("1.2.3.4")
	pruned := mgr.Prune(0)
	if len(pruned) != 1 {
		t.Errorf("expected one device pruned, got %d", len(pruned))
	}
	if _, ok := mgr.Get("1.2.3.4"); ok {
		t.Errorf("expected device to be removed after prune")
	}
}

func TestManagerForEachIPAndHas(t *testing.T) {
	mgr := NewManager(1000)
	ips := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	for _, ip := range ips {
		mgr.AddDevice(ip)
	}

	for _, ip := range ips {
		if !mgr.Has(ip) {
			t.Errorf("expected mgr.Has(%s) to be true", ip)
		}
	}
	if mgr.Has("10.0.0.99") {
		t.Errorf("expected mgr.Has('10.0.0.99') to be false")
	}

	visited := make(map[string]bool)
	mgr.ForEachIP(func(ip string) {
		visited[ip] = true
	})

	if len(visited) != len(ips) {
		t.Errorf("expected %d visited IPs, got %d", len(ips), len(visited))
	}
	for _, ip := range ips {
		if !visited[ip] {
			t.Errorf("expected IP %s to be visited", ip)
		}
	}
}
