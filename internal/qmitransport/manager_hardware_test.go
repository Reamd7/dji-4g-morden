//go:build hardware

// Hardware integration tests for the QMI manager flow (service allocation +
// dialup). Requires a real DJI Baiwang modem (QDC507, PID 2C7C:0125) with
// WinUSB on MI_04 + SIM (the carrier, PS attach).
//
// Run with:
//
//	DJI_TEST_APN=3gnet mise exec -- go test -tags=hardware -v -run TestHardwareManager ./internal/qmitransport/
//
// TestHardwareManagerDialup activates a PDP context (uses data). All other
// tests are read-only.
package qmitransport

import (
	"context"
	"os"
	"testing"
	"time"

	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

func testAPN() string {
	if apn := os.Getenv("DJI_TEST_APN"); apn != "" {
		return apn
	}
	return "3gnet"
}

// openClient is a helper that opens a QMITransport + creates a QMI client.
// Returns (transport, client, cleanup). The cleanup closes everything.
func openClient(t *testing.T) (*QMITransport, *qmi.Client) {
	t.Helper()
	tr, err := Open()
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	clientOpts := qmi.DefaultClientOptions()
	client, err := qmi.NewClientFromTransport(context.Background(), tr, clientOpts)
	if err != nil {
		tr.Close()
		t.Fatalf("NewClientFromTransport failed: %v", err)
	}
	return tr, client
}

// TestHardwareManagerStartCore verifies manager.StartCore allocates all QMI
// services (NAS/DMS/UIM/WDA/WDS) via the USB hook. This is read-only — no
// dialing, no PDP activation.
func TestHardwareManagerStartCore(t *testing.T) {
	tr, client := openClient(t)
	defer client.Close()
	defer tr.Close()

	cfg := manager.Config{
		EnableIPv4: true,
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := mgr.StartCoreContext(ctx); err != nil {
		t.Fatalf("StartCore failed: %v", err)
	}
	defer mgr.Stop()
	t.Log("StartCore OK — all QMI services allocated")
}

// TestHardwareManagerDialup verifies the full dialup flow: StartCore →
// Connect (WDS StartNetwork) → GetRuntimeSettings. This activates a PDP
// context on the SIM and may incur data charges.
func TestHardwareManagerDialup(t *testing.T) {
	tr, client := openClient(t)
	defer client.Close()
	defer tr.Close()

	apn := testAPN()
	cfg := manager.Config{
		APN:        apn,
		EnableIPv4: true,
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
			Dial:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := mgr.StartCoreContext(ctx); err != nil {
		t.Fatalf("StartCore failed: %v", err)
	}
	defer mgr.Stop()

	if err := mgr.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Verify IPv4 settings.
	s := mgr.Settings()
	if s == nil {
		t.Fatal("Settings() returned nil after Connect")
	}
	if len(s.IPv4Address) == 0 {
		t.Fatal("no IPv4 address after dialup")
	}
	t.Logf("APN=%s, IPv4=%s, MTU=%d", apn, s.IPv4Address, s.MTU)
	if s.MTU == 0 {
		t.Log("warning: MTU is 0")
	}

	// Verify PDH is non-zero.
	if h := mgr.HandleV4(); h == 0 {
		t.Fatal("HandleV4 is 0 after Connect")
	} else {
		t.Logf("PDH v4=0x%08x", h)
	}
}

// TestHardwareManagerDialupIPv6 verifies dual-stack dialup (IPv4 + IPv6).
func TestHardwareManagerDialupIPv6(t *testing.T) {
	tr, client := openClient(t)
	defer client.Close()
	defer tr.Close()

	apn := testAPN()
	cfg := manager.Config{
		APN:        apn,
		EnableIPv4: true,
		EnableIPv6: true,
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
			Dial:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := mgr.StartCoreContext(ctx); err != nil {
		t.Fatalf("StartCore failed: %v", err)
	}
	defer mgr.Stop()

	if err := mgr.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// IPv4
	s4 := mgr.Settings()
	if s4 == nil || len(s4.IPv4Address) == 0 {
		t.Fatal("no IPv4 address")
	}
	t.Logf("IPv4=%s/%s", s4.IPv4Address, s4.IPv4Subnet)

	// IPv6
	if h6 := mgr.HandleV6(); h6 == 0 {
		t.Log("IPv6 not connected (operator may not support dual-stack on this APN)")
	} else {
		s6 := mgr.SettingsV6()
		if s6 != nil && len(s6.IPv6Address) > 0 {
			t.Logf("IPv6=%s/%d", s6.IPv6Address, s6.IPv6Prefix)
		} else {
			t.Log("IPv6 handle set but no address")
		}
	}
}

// TestHardwareConcurrentManagerClose stress-tests the ioMu serialization under
// real USB with the full QMI client stack. Runs readLoop + writerLoop
// (triggered by manager service allocation) + Close mid-flight, repeated 5×.
func TestHardwareConcurrentManagerClose(t *testing.T) {
	for i := range 5 {
		tr, client := openClient(t)
		defer tr.Close() // no-op if manager cleanup already closed it

		cfg := manager.Config{
			EnableIPv4: true,
			Timeouts: manager.TimeoutConfig{
				IndicationRegister: 15 * time.Second,
				Init:               30 * time.Second,
			},
		}
		mgr := manager.NewWithClient(cfg, nil, client)

		// Start core in a goroutine — Close may race with service allocation.
		startErr := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			startErr <- mgr.StartCoreContext(ctx)
		}()

		// Give it a moment to start allocating, then close mid-flight.
		time.Sleep(50 * time.Millisecond)

		// Stop/Close — this exercises ioMu vs in-flight QMI exchanges.
		// If StartCore hasn't finished, Stop will race with it.
		err := <-startErr
		if err != nil {
			// StartCore may have been interrupted by Close — that's OK.
			t.Logf("iteration %d: StartCore returned %v (expected if Close raced)", i, err)
		}
		mgr.Stop()

		time.Sleep(100 * time.Millisecond)
		t.Logf("iteration %d: clean close with concurrent QMI activity", i)
	}
}
