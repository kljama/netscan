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
