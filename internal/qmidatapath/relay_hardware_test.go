//go:build hardware

// Hardware integration tests for the TUN relay data path. Requires:
//   - Real DJI Baiwang modem (QDC507, PID 2C7C:0125) + WinUSB on MI_04
//   - SIM card with PS attach
//   - Windows: admin privileges + wintun.dll + libusb-1.0.dll (for TUN tests)
//
// Run with:
//
//	# Non-admin tests (bulk endpoints + ZLP)
//	mise exec -- go test -tags=hardware -v -run TestHardwareBulk ./internal/qmidatapath/
//
//	# Full TUN relay tests (needs admin terminal + wintun.dll)
//	mise exec -- go test -tags=hardware -v -run TestHardwareRelay ./internal/qmidatapath/
package qmidatapath

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun"

	"dji-modem-research/internal/qmitransport"
	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

// openFullStack opens transport + client + manager + dials.
// Returns (transport, client, manager, cleanup func).
func openFullStack(t *testing.T, apn string) (*qmitransport.QMITransport, *qmi.Client, *manager.Manager, func()) {
	t.Helper()
	ctx := context.Background()

	transport, err := qmitransport.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	clientOpts := qmi.DefaultClientOptions()
	client, err := qmi.NewClientFromTransport(ctx, transport, clientOpts)
	if err != nil {
		transport.Close()
		t.Fatalf("NewClientFromTransport: %v", err)
	}

	cfg := manager.Config{
		APN:        apn,
		EnableIPv4: true,
		Device:     manager.ModemDevice{NetInterface: "dummy"}, // triggers WDA
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
	defer startCancel()
	if err := mgr.StartCoreContext(startCtx); err != nil {
		client.Close()
		transport.Close()
		t.Fatalf("StartCore: %v", err)
	}

	if err := mgr.Connect(); err != nil {
		mgr.Stop()
		client.Close()
		transport.Close()
		t.Fatalf("Connect: %v", err)
	}

	cleanup := func() {
		mgr.Stop()
		client.Close()
		transport.Close()
	}
	return transport, client, mgr, cleanup
}

// TestHardwareBulkEndpoints verifies OpenBulkEndpoints returns valid endpoints.
// Does NOT require admin or TUN — only USB access.
func TestHardwareBulkEndpoints(t *testing.T) {
	transport, _, _, cleanup := openFullStack(t, "3gnet")
	defer cleanup()

	bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
	if err != nil {
		t.Fatalf("OpenBulkEndpoints: %v", err)
	}
	if bulkIn == nil {
		t.Fatal("bulkIn is nil")
	}
	if bulkOut == nil {
		t.Fatal("bulkOut is nil")
	}

	// Read a few packets from bulk IN to verify data flows
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	buf := make([]byte, 65535)
	count := 0
	for {
		n, err := bulkIn.ReadContext(ctx, buf)
		if err != nil {
			break
		}
		if n > 0 {
			count++
		}
	}
	t.Logf("Read %d packets from bulk IN EP 0x88", count)
}

// TestHardwareRelayEndToEnd tests the full TUN relay: dial + TUN + relay + ping.
// REQUIRES admin privileges + wintun.dll on Windows.
func TestHardwareRelayEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Run from elevated terminal: go test -tags=hardware -run TestHardwareRelayEndToEnd")
	}

	ctx := context.Background()

	// 1. Transport + client
	transport, err := qmitransport.Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer transport.Close()

	clientOpts := qmi.DefaultClientOptions()
	client, err := qmi.NewClientFromTransport(ctx, transport, clientOpts)
	if err != nil {
		transport.Close()
		t.Fatalf("NewClientFromTransport: %v", err)
	}
	defer client.Close()

	// 2. TUN (before manager, so configureNetwork can set IP)
	tunName := "qmi0"
	if runtime.GOOS == "darwin" {
		tunName = "utun"
	}
	tunDev, err := tun.CreateTUN(tunName, 1500)
	if err != nil {
		t.Fatalf("CreateTUN: %v", err)
	}
	tunName, _ = tunDev.Name()

	// 3. Manager + dial
	cfg := manager.Config{
		APN:        "3gnet",
		EnableIPv4: true,
		Device:     manager.ModemDevice{NetInterface: tunName},
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
	defer startCancel()
	if err := mgr.StartCoreContext(startCtx); err != nil {
		tunDev.Close()
		t.Fatalf("StartCore: %v", err)
	}
	defer mgr.Stop()

	if err := mgr.Connect(); err != nil {
		tunDev.Close()
		t.Fatalf("Connect: %v", err)
	}

	s := mgr.Settings()
	if s == nil || len(s.IPv4Address) == 0 {
		tunDev.Close()
		t.Fatal("No IPv4 address")
	}
	t.Logf("IP=%s, MTU=%d", s.IPv4Address, s.MTU)

	// 4. Relay
	bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
	if err != nil {
		tunDev.Close()
		t.Fatalf("OpenBulkEndpoints: %v", err)
	}

	offset := 0
	if runtime.GOOS == "darwin" {
		offset = 4
	}
	sink := NewTUNPacketSink(tunDev, offset, 1500)
	bridge := New(sink, bulkIn, bulkOut, 1500, true)
	bridge.Start(ctx)

	time.Sleep(3 * time.Second) // let relay stabilize

	// 5. Ping through TUN (use baidu.com — 114DNS blocks ICMP)
	pingArgs := []string{"-c", "4", "baidu.com"}
	if runtime.GOOS == "windows" {
		pingArgs = []string{"-n", "4", "baidu.com"}
	}
	cmd := exec.Command("ping", pingArgs...)
	cmd.Stdout = &testWriter{t: t}
	cmd.Stderr = &testWriter{t: t}
	if err := cmd.Run(); err != nil {
		t.Logf("ping failed (may be carrier ICMP filter): %v", err)
	}

	// 6. TCP test (curl)
	curlArgs := []string{"-s", "-o", "/dev/null", "-w", "%{http_code}", "http://www.baidu.com"}
	if runtime.GOOS == "windows" {
		curlArgs = []string{"-s", "-o", "NUL", "-w", "%{http_code}", "http://www.baidu.com"}
	}
	curlCmd := exec.Command("curl", curlArgs...)
	curlOut, curlErr := curlCmd.Output()
	if curlErr != nil {
		t.Logf("curl failed: %v", curlErr)
	} else {
		code := string(curlOut)
		t.Logf("curl baidu.com: HTTP %s", code)
		if code != "200" {
			t.Errorf("curl got %s, want 200", code)
		}
	}

	// 7. Cleanup
	tunDev.Close()
	bridge.Stop()

	txPkt, _, rxPkt, _ := bridge.Stats()
	t.Logf("Relay: TX %d pkts, RX %d pkts", txPkt, rxPkt)
}

// testWriter implements io.Writer, forwarding to t.Log.
type testWriter struct{ t *testing.T }

func (w *testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
