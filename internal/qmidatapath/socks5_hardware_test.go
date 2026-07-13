//go:build hardware

// Hardware integration tests for the netstack + SOCKS5 data path. Requires:
//   - Real DJI Baiwang modem (QDC507, PID 2C7C:0125) + libusb access on MI_04
//   - SIM card with PS attach
//   - curl on PATH
//   - No admin privileges needed (netstack is pure userspace)
//
// Run with:
//
//	mise exec -- go test -tags=hardware -v -run TestHardwareNetstack ./internal/qmidatapath/
package qmidatapath

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// setupNetstackSOCKS5 brings up the full SOCKS5-over-4G stack (transport →
// manager → bulk EP → netstack sink → bridge → SOCKS5 listener) and returns
// the bridge (for stats), whether IPv6 was assigned, and a teardown func.
// The caller drives curl through socksAddr.
func setupNetstackSOCKS5(t *testing.T, socksAddr string) (bridge *Bridge, hasIPv6 bool, teardown func()) {
	t.Helper()
	apn := os.Getenv("DJI_TEST_APN")
	if apn == "" {
		apn = "3gnet"
	}
	transport, _, mgr, stackCleanup := openFullStack(t, apn)

	s := mgr.Settings()
	if s == nil || len(s.IPv4Address) == 0 {
		stackCleanup()
		t.Fatal("no IPv4 address from dialup")
	}

	bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
	if err != nil {
		stackCleanup()
		t.Fatalf("OpenBulkEndpoints: %v", err)
	}

	ipBytes := s.IPv4Address.To4()
	localIP := netip.AddrFrom4([4]byte{ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3]})
	var v6Addr netip.Addr
	if s6 := mgr.SettingsV6(); s6 != nil && len(s6.IPv6Address) > 0 {
		if v6, ok := netip.AddrFromSlice(s6.IPv6Address); ok {
			v6Addr = v6.Unmap()
		}
	}
	sink, err := NewNetstackPacketSink(localIP, int(s.MTU), v6Addr.IsValid(), v6Addr)
	if err != nil {
		stackCleanup()
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}
	var dns []netip.Addr
	if d, ok := netip.AddrFromSlice(s.IPv4DNS1); ok {
		dns = append(dns, d.Unmap())
	}
	if len(dns) > 0 {
		sink.SetDNSServers(dns)
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := New(sink, bulkIn, bulkOut, int(s.MTU), true)
	if err := b.Start(ctx); err != nil {
		cancel()
		sink.Close()
		stackCleanup()
		t.Fatalf("Bridge.Start: %v", err)
	}
	go func() {
		if err := RunSOCKS5(ctx, sink, socksAddr); err != nil && ctx.Err() == nil {
			t.Logf("RunSOCKS5: %v", err)
		}
	}()
	time.Sleep(500 * time.Millisecond) // let the listener bind

	teardown = func() {
		cancel()
		time.Sleep(150 * time.Millisecond)
		b.Stop()
		sink.Close()
		stackCleanup()
	}
	return b, v6Addr.IsValid(), teardown
}

// curlViaSOCKS5 runs curl through the SOCKS5 proxy and returns the HTTP code.
func curlViaSOCKS5(t *testing.T, socksAddr, url string, wantV6 bool) string {
	t.Helper()
	args := []string{"-s", "-o", os.DevNull, "-w", "%{http_code}",
		"--socks5-hostname", socksAddr, "--max-time", "30"}
	if wantV6 {
		args = append(args, "-6")
	}
	args = append(args, url)
	out, err := exec.Command("curl", args...).Output()
	if err != nil {
		t.Fatalf("curl %s: %v", url, err)
	}
	return strings.TrimSpace(string(out))
}

// TestHardwareNetstackSOCKS5 verifies SOCKS5-over-4G (netstack backend): an
// HTTP request through the proxy succeeds and relay TX/RX counters grow.
// Subplan 02 acceptance: HTTP response received + packet round-trip.
// No admin privileges needed.
func TestHardwareNetstackSOCKS5(t *testing.T) {
	const socksAddr = "127.0.0.1:1080"
	bridge, _, teardown := setupNetstackSOCKS5(t, socksAddr)
	defer teardown()

	baseTx, _, baseRx, _ := bridge.Stats()

	code := curlViaSOCKS5(t, socksAddr, "http://www.baidu.com", false)
	if code != "200" && code != "302" {
		t.Fatalf("HTTP via SOCKS5: want 200/302, got %q", code)
	}

	txP, _, rxP, _ := bridge.Stats()
	if txP <= baseTx || rxP <= baseRx {
		t.Errorf("relay traffic did not grow: TX %d→%d, RX %d→%d", baseTx, txP, baseRx, rxP)
	}
	t.Logf("SOCKS5 HTTP %s; relay delta TX %+d RX %+d pkts", code, txP-baseTx, rxP-baseRx)
}

// TestHardwareNetstackSOCKS5IPv6 verifies an IPv6 site is reachable through
// the SOCKS5 proxy over the 4G link — the IPv6 dual-stack end-to-end check
// (subplan 03 acceptance: "IPv6 站点可达 curl -6 经 SOCKS5").
func TestHardwareNetstackSOCKS5IPv6(t *testing.T) {
	const socksAddr = "127.0.0.1:1081"
	_, hasIPv6, teardown := setupNetstackSOCKS5(t, socksAddr)
	defer teardown()

	if !hasIPv6 {
		t.Skip("no IPv6 address assigned; carrier did not provide IPv6")
	}

	code := curlViaSOCKS5(t, socksAddr, "https://www.baidu.com", true)
	if code != "200" && code != "302" {
		t.Fatalf("IPv6 HTTP via SOCKS5: want 200/302, got %q", code)
	}
	t.Logf("IPv6 via SOCKS5 HTTP %s", code)
}

// TestHardwareNetstackGoroutineLeak verifies the relay + SOCKS5 goroutines
// exit after teardown (subplan 02 acceptance: no goroutine leak).
func TestHardwareNetstackGoroutineLeak(t *testing.T) {
	const socksAddr = "127.0.0.1:1082"
	before := runtime.NumGoroutine()
	_, _, teardown := setupNetstackSOCKS5(t, socksAddr)

	curlViaSOCKS5(t, socksAddr, "http://www.baidu.com", false) // drive traffic
	teardown()

	time.Sleep(500 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+4 {
		t.Errorf("goroutine leak: %d before, %d after teardown", before, after)
	}
	t.Logf("goroutines: %d before → %d after teardown", before, after)
}
