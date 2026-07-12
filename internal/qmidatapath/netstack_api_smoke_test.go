//go:build !hardware

package qmidatapath

import (
	"context"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

// TestNetstackAPISmoke verifies the gVisor netstack API signatures we depend on.
func TestNetstackAPISmoke(t *testing.T) {
	// 1. channel.New
	ep := channel.New(64, 1500, "")
	if ep == nil {
		t.Fatal("channel.New returned nil")
	}

	// 2. ReadContext with timeout (should return nil, not block)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	pkt := ep.ReadContext(ctx)
	if pkt != nil {
		t.Fatal("expected nil from ReadContext on empty channel with expired ctx")
	}

	// 3. stack.New
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
			ipv6.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol,
			udp.NewProtocol,
		},
	})
	if s == nil {
		t.Fatal("stack.New returned nil")
	}

	// 4. CreateNIC
	nicID := tcpip.NICID(1)
	if terr := s.CreateNIC(nicID, ep); terr != nil {
		t.Fatalf("CreateNIC: %v", terr)
	}

	// 5. AddProtocolAddress (this version uses AddProtocolAddress, not AddAddress)
	addrWithPrefix := tcpip.AddressWithPrefix{
		Address:   tcpip.AddrFrom4([4]byte{10, 0, 0, 1}),
		PrefixLen: 32,
	}
	if terr := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: addrWithPrefix,
	}, stack.AddressProperties{}); terr != nil {
		t.Fatalf("AddProtocolAddress: %v", terr)
	}

	// 6. SetRouteTable
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
	})

	// 7. InjectInbound — inject a test packet
	injectPkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(makeIPv4Packet(64)),
	})
	ep.InjectInbound(ipv4.ProtocolNumber, injectPkt)

	// 8. ep.Close — should not panic
	ep.Close()

	// 9. stack.Close
	s.Close()

	// 10. Verify gonet.DialContextTCP signature exists
	_ = func(ctx context.Context, s *stack.Stack, addr tcpip.FullAddress, net tcpip.NetworkProtocolNumber) (*gonet.TCPConn, error) {
		return gonet.DialContextTCP(ctx, s, addr, net)
	}

	t.Log("All gVisor netstack API signatures verified")
}
