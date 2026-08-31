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
	ips := []string{"192.168.1.1", "192.168.1.2", "10.0.0.1"}

	for _, ip := range ips {
		mgr.AddDevice(ip)
	}

	// Test Has
	for _, ip := range ips {
		if !mgr.Has(ip) {
			t.Errorf("expected Has(%s) to be true", ip)
		}
	}

	if mgr.Has("172.16.0.1") {
		t.Errorf("expected Has(172.16.0.1) to be false")
	}

	// Test ForEachIP
	visited := make(map[string]bool)
	mgr.ForEachIP(func(ip string) {
		visited[ip] = true
	})

	if len(visited) != len(ips) {
		t.Errorf("expected %d visited IPs, got %d", len(ips), len(visited))
	}

	for _, ip := range ips {
		if !visited[ip] {
			t.Errorf("expected ForEachIP to visit %s", ip)
		}
	}

	// Test ForEachDevice (verifying metadata preservation)
	mgr.UpdateDeviceSNMP("192.168.1.1", "router1", "Cisco IOS")
	visitedDevs := make(map[string]Device)
	mgr.ForEachDevice(func(dev Device) {
		visitedDevs[dev.IP] = dev
	})

	if len(visitedDevs) != len(ips) {
		t.Errorf("expected %d visited Devices, got %d", len(ips), len(visitedDevs))
	}

	dev1, ok := visitedDevs["192.168.1.1"]
	if !ok || dev1.Hostname != "router1" || dev1.SysDescr != "Cisco IOS" {
		t.Errorf("expected device metadata preserved, got %+v", dev1)
	}
}
