package manager

import (
	"testing"
)

// TestNewWithClientSetsHook verifies that NewWithClient installs the
// openClientAndAllocateServicesHook, bypassing the default device-opening
// path (openClientAndAllocateServices → /dev/cdc-wdm0).
//
// We pass nil for the client — the hook closure captures it but we never
// execute the hook in this test, only verify it was set.
func TestNewWithClientSetsHook(t *testing.T) {
	cfg := Config{APN: "test", EnableIPv4: true}
	mgr := NewWithClient(cfg, nil, nil)

	if mgr.openClientAndAllocateServicesHook == nil {
		t.Fatal("NewWithClient did not set openClientAndAllocateServicesHook")
	}
}

// TestNewWithClientPreservesConfig verifies that NewWithClient passes the
// config through normalizeConfig (same as New).
func TestNewWithClientPreservesConfig(t *testing.T) {
	cfg := Config{APN: "3gnet", EnableIPv4: true}
	mgr := NewWithClient(cfg, nil, nil)

	if mgr.cfg.APN != "3gnet" {
		t.Fatalf("APN = %q, want %q", mgr.cfg.APN, "3gnet")
	}
	if !mgr.cfg.EnableIPv4 {
		t.Fatal("EnableIPv4 should be true")
	}
	// normalizeConfig should have set defaults.
	if mgr.cfg.Timeouts.Init <= 0 {
		t.Fatal("Timeouts.Init should be set by normalizeConfig")
	}
}

// TestNewWithClientDoesNotSetControlPath verifies that the USB injection path
// doesn't accidentally set a Linux device path that would be used if the hook
// were somehow bypassed.
func TestNewWithClientDoesNotSetControlPath(t *testing.T) {
	mgr := NewWithClient(Config{}, nil, nil)
	if mgr.cfg.Device.ControlPath != "" {
		t.Fatalf("ControlPath = %q, want empty (USB transport doesn't use device paths)",
			mgr.cfg.Device.ControlPath)
	}
}
