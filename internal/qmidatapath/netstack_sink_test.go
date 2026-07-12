//go:build !hardware

package qmidatapath

import (
	"context"
	"net/netip"
	"testing"
	"time"
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
