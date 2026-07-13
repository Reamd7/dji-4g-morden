//go:build !hardware

package qmidatapath

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

// TestNetstackSinkCreateClose verifies NetstackPacketSink can be created and closed.
func TestNetstackSinkCreateClose(t *testing.T) {
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500,
		false, // IPv6 disabled
		netip.Addr{},
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}

	if sink.Name() != "netstack" {
		t.Errorf("Name() = %q, want %q", sink.Name(), "netstack")
	}

	// Close should not panic
	if err := sink.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestNetstackSinkDualStack verifies IPv6 dual-stack creation.
func TestNetstackSinkDualStack(t *testing.T) {
	v6Addr := netip.AddrFrom16([16]byte{0x24, 0x08, 0x84, 0x56, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01})
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500,
		true, v6Addr,
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink dual-stack: %v", err)
	}
	defer sink.Close()
}

// TestNetstackSinkReadPacketCancel verifies ReadPacket respects context cancellation.
// After Close(), ReadPacket should return an error (channel closed).
func TestNetstackSinkReadPacketCancel(t *testing.T) {
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500,
		false, netip.Addr{},
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}

	// ReadPacket should block (no packets in channel)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	pkt, err := sink.ReadPacket(ctx)
	if err == nil && pkt != nil {
		t.Fatal("expected timeout/close error, got packet")
	}

	// After Close, ReadPacket should return error immediately
	sink.Close()
	readCtx, readCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer readCancel()
	_, err = sink.ReadPacket(readCtx)
	if err == nil {
		t.Fatal("expected error after Close, got nil")
	}
}

// TestNetstackSinkWritePacket verifies WritePacket accepts valid IP packets
// and rejects garbage. Uses a fresh sink (channel absorbs the injected packet
// silently via the stack's inbound path).
func TestNetstackSinkWritePacket(t *testing.T) {
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500,
		false, netip.Addr{},
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}
	defer sink.Close()

	// Valid IPv4 packet — should not error
	v4Pkt := makeIPv4Packet(64)
	if err := sink.WritePacket(v4Pkt); err != nil {
		t.Errorf("WritePacket IPv4: %v", err)
	}

	// Empty packet — should be a no-op
	if err := sink.WritePacket(nil); err != nil {
		t.Errorf("WritePacket(nil): %v", err)
	}

	// Garbage (version 0) — should error
	garbage := []byte{0x00, 0x01, 0x02, 0x03}
	if err := sink.WritePacket(garbage); err == nil {
		t.Error("WritePacket(garbage): expected error, got nil")
	}
}

// TestNetstackSinkImplementsPacketSink verifies NetstackPacketSink satisfies
// the PacketSink interface at compile time.
func TestNetstackSinkImplementsPacketSink(t *testing.T) {
	var _ PacketSink = (*NetstackPacketSink)(nil)
}

// TestNetstackDialerRejectsUnsupported verifies the dialer rejects non-TCP networks.
func TestNetstackDialerRejectsUnsupported(t *testing.T) {
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500,
		false, netip.Addr{},
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}
	defer sink.Close()

	dial := sink.NetstackDialer()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err = dial(ctx, "raw", "1.2.3.4:80")
	if err == nil {
		t.Error("expected error for raw socket, got nil")
	}
}

// TestSetDNSServers verifies SetDNSServers stores the servers, that
// resolver4G errors before they are set, and that re-setting invalidates
// the cached resolver.
func TestSetDNSServers(t *testing.T) {
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500, false, netip.Addr{},
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}
	defer sink.Close()

	// No servers yet → resolver4G must error.
	if _, err := sink.resolver4G(); err == nil {
		t.Fatal("resolver4G before SetDNSServers: want error, got nil")
	}

	servers := []netip.Addr{
		netip.AddrFrom4([4]byte{114, 114, 114, 114}),
		netip.AddrFrom4([4]byte{8, 8, 8, 8}),
	}
	sink.SetDNSServers(servers)
	if got := len(sink.dnsServers); got != 2 {
		t.Errorf("dnsServers len = %d, want 2", got)
	}
	if sink.dnsServers[0] != servers[0] {
		t.Errorf("dnsServers[0] = %v, want %v", sink.dnsServers[0], servers[0])
	}

	// With servers set, resolver4G builds a usable resolver (no query issued).
	r, err := sink.resolver4G()
	if err != nil {
		t.Fatalf("resolver4G after SetDNSServers: %v", err)
	}
	if r == nil {
		t.Fatal("resolver4G returned nil resolver")
	}

	// Re-setting invalidates the cache (resolverOnce reset) + stores new servers.
	sink.SetDNSServers([]netip.Addr{netip.AddrFrom4([4]byte{1, 1, 1, 1})})
	if _, err := sink.resolver4G(); err != nil {
		t.Fatalf("resolver4G after re-Set: %v", err)
	}
	if len(sink.dnsServers) != 1 || sink.dnsServers[0] != netip.AddrFrom4([4]byte{1, 1, 1, 1}) {
		t.Errorf("dnsServers after re-Set = %v, want [1.1.1.1]", sink.dnsServers)
	}
}

// TestResolver4GIPv6Server verifies an IPv6 DNS server constructs the resolver
// without error (validates the v6 proto branch; no network I/O).
func TestResolver4GIPv6Server(t *testing.T) {
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500, true,
		netip.MustParseAddr("2001:db8::1"),
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}
	defer sink.Close()

	sink.SetDNSServers([]netip.Addr{netip.MustParseAddr("2001:4860:4860::8888")})
	if _, err := sink.resolver4G(); err != nil {
		t.Fatalf("resolver4G ipv6 server: %v", err)
	}
}

// TestPickIP verifies address-family selection by network suffix.
func TestPickIP(t *testing.T) {
	v4 := netip.AddrFrom4([4]byte{1, 2, 3, 4})
	v6 := netip.MustParseAddr("2001:db8::1")
	ips := []netip.Addr{v4, v6}

	cases := []struct {
		network string
		want    netip.Addr
	}{
		{"tcp4", v4},
		{"tcp6", v6},
		{"udp4", v4},
		{"udp6", v6},
		{"tcp", ips[0]}, // unspecified family → first
		{"udp", ips[0]},
	}
	for _, c := range cases {
		if got := pickIP(ips, c.network); got != c.want {
			t.Errorf("pickIP(_, %q) = %v, want %v", c.network, got, c.want)
		}
	}

	if got := pickIP(nil, "tcp4"); got.IsValid() {
		t.Errorf("pickIP(nil) = %v, want invalid", got)
	}
	// Requested family absent → falls back to first available.
	if got := pickIP([]netip.Addr{v4}, "tcp6"); got != v4 {
		t.Errorf("pickIP([v4], tcp6) = %v, want v4 fallback", got)
	}
}

// TestNetstackSinkUpRoundTrip verifies the uplink path: netstack emits a
// packet (TCP SYN from a dial attempt) → channel → ReadPacket reads it out.
func TestNetstackSinkUpRoundTrip(t *testing.T) {
	sink, err := NewNetstackPacketSink(
		netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500, false, netip.Addr{},
	)
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}
	defer sink.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Trigger an outbound packet: a TCP connect sends a SYN through netstack.
	go func() {
		gonet.DialContextTCP(ctx, sink.stk, tcpip.FullAddress{
			NIC:  sink.nicID,
			Addr: tcpip.AddrFrom4([4]byte{93, 184, 216, 34}),
			Port: 80,
		}, ipv4.ProtocolNumber)
	}()

	pkt, err := sink.ReadPacket(ctx)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if len(pkt) == 0 {
		t.Fatal("ReadPacket got empty packet")
	}
	if pkt[0]>>4 != 4 {
		t.Fatalf("ReadPacket got non-IPv4 packet (first byte %02x)", pkt[0])
	}
}
