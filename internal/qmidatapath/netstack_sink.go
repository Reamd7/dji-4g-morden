package qmidatapath

import (
	"context"
	"fmt"
	"net"
	"net/netip"

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

// NetstackPacketSink runs a gVisor userspace TCP/IP stack.
// USB bulk packets ↔ Go channel ↔ netstack ↔ (SOCKS5 server).
//
// The link layer is a Go channel (channel.Endpoint), NOT a TUN device.
// This means: no admin privileges, no platform-specific code, no routing
// changes — pure Go userspace networking.
type NetstackPacketSink struct {
	stk   *stack.Stack
	ep    *channel.Endpoint
	nicID tcpip.NICID
	addr  tcpip.Address
	mtu   uint32
}

// NewNetstackPacketSink creates a netstack-backed PacketSink.
//
// localIP: modem-assigned IPv4 (from mgr.Settings().IPv4Address)
// mtu: typically 1500 (from mgr.Settings().MTU)
// enableIPv6: if true, IPv6 protocol is also registered (dual-stack)
// ipv6Addr: optional IPv6 address for dual-stack (pass empty to skip)
func NewNetstackPacketSink(localIP netip.Addr, mtu int, enableIPv6 bool, ipv6Addr netip.Addr) (*NetstackPacketSink, error) {
	if !localIP.IsValid() {
		return nil, fmt.Errorf("netstack: invalid local IP")
	}

	// 1. Channel link endpoint — this IS our "TUN replacement"
	const channelSize = 256
	ep := channel.New(channelSize, uint32(mtu), "")

	// 2. Create stack with our channel as link layer
	netProtos := []stack.NetworkProtocolFactory{ipv4.NewProtocol}
	if enableIPv6 {
		netProtos = append(netProtos, ipv6.NewProtocol)
	}
	s := stack.New(stack.Options{
		NetworkProtocols:   netProtos,
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	// 3. Create NIC on our channel endpoint
	nicID := tcpip.NICID(1)
	if terr := s.CreateNIC(nicID, ep); terr != nil {
		return nil, fmt.Errorf("netstack: CreateNIC: %v", terr)
	}

	// 4. Default route → our channel (all outbound traffic goes to USB)
	routes := []tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
	}
	if enableIPv6 {
		routes = append(routes, tcpip.Route{Destination: header.IPv6EmptySubnet, NIC: nicID})
	}
	s.SetRouteTable(routes)

	// 5. Set the modem-assigned IP address
	addr := tcpip.AddrFromSlice(localIP.AsSlice())
	if terr := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   addr,
			PrefixLen: 32,
		},
	}, stack.AddressProperties{}); terr != nil {
		return nil, fmt.Errorf("netstack: AddProtocolAddress v4: %v", terr)
	}

	// 6. Optional IPv6 address
	if enableIPv6 && ipv6Addr.IsValid() {
		v6Addr := tcpip.AddrFromSlice(ipv6Addr.AsSlice())
		if terr := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
			Protocol: ipv6.ProtocolNumber,
			AddressWithPrefix: tcpip.AddressWithPrefix{
				Address:   v6Addr,
				PrefixLen: 64,
			},
		}, stack.AddressProperties{}); terr != nil {
			return nil, fmt.Errorf("netstack: AddProtocolAddress v6: %v", terr)
		}
	}

	return &NetstackPacketSink{
		stk:   s,
		ep:    ep,
		nicID: nicID,
		addr:  addr,
		mtu:   uint32(mtu),
	}, nil
}

// Stack returns the underlying gVisor stack (for SOCKS5 dialer).
func (s *NetstackPacketSink) Stack() *stack.Stack {
	return s.stk
}

// ReadPacket: netstack → USB (uplink).
// Blocks until netstack emits a packet on the channel, or ctx is canceled.
// netstack emits a packet when an app (e.g. SOCKS5 → gonet.Dial) sends data.
func (s *NetstackPacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
	pkt := s.ep.ReadContext(ctx)
	if pkt == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("netstack: channel closed")
	}
	defer pkt.DecRef()
	return pkt.Data().AsRange().ToSlice(), nil
}

// WritePacket: USB → netstack (downlink).
// Injects a raw IP packet (from modem) into netstack's inbound path.
func (s *NetstackPacketSink) WritePacket(pkt []byte) error {
	if len(pkt) == 0 {
		return nil
	}
	// Determine protocol from IP version
	var proto tcpip.NetworkProtocolNumber
	switch pkt[0] >> 4 {
	case 4:
		proto = ipv4.ProtocolNumber
	case 6:
		proto = ipv6.ProtocolNumber
	default:
		return fmt.Errorf("netstack: unknown IP version %d", pkt[0]>>4)
	}
	// Copy into a fresh buffer — gVisor PacketBuffer owns its payload
	pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(append([]byte(nil), pkt...)),
	})
	defer pkb.DecRef()
	s.ep.InjectInbound(proto, pkb)
	return nil
}

func (s *NetstackPacketSink) Name() string {
	return "netstack"
}

// Close releases netstack resources.
// ep.Close() makes pending ReadContext return nil → ReadPacket returns error
// → Bridge's sinkToModem goroutine exits naturally.
func (s *NetstackPacketSink) Close() error {
	s.ep.Close()
	s.stk.Close()
	return nil
}

// NetstackDialer returns a Dial function suitable for SOCKS5 server's
// Config.Dial. Connections dialed through this function go through the
// gVisor netstack → channel → USB → modem (NOT the host network).
//
// For Phase 2, DNS resolution uses Go's standard resolver (host network).
// Phase 3 can upgrade to 4G DNS.
func (s *NetstackPacketSink) NetstackDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("netstack dialer: bad addr %q: %w", addr, err)
		}
		// DNS resolution via host resolver (Phase 2; upgrade in Phase 3)
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("netstack dialer: resolve %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("netstack dialer: no IP for %s", host)
		}
		portNum, err := net.LookupPort("tcp", port)
		if err != nil {
			return nil, fmt.Errorf("netstack dialer: bad port %q: %w", port, err)
		}

		ipAddr, _ := netip.AddrFromSlice(ips[0])
		fullAddr := tcpip.FullAddress{
			NIC:  s.nicID,
			Addr: tcpip.AddrFromSlice(ipAddr.AsSlice()),
			Port: uint16(portNum),
		}

		switch network {
		case "tcp", "tcp4":
			return gonet.DialContextTCP(ctx, s.stk, fullAddr, ipv4.ProtocolNumber)
		case "tcp6":
			return gonet.DialContextTCP(ctx, s.stk, fullAddr, ipv6.ProtocolNumber)
		case "udp", "udp4":
			c, err := gonet.DialUDP(s.stk, nil, &fullAddr, ipv4.ProtocolNumber)
			if err != nil {
				return nil, err
			}
			return c, nil
		case "udp6":
			c, err := gonet.DialUDP(s.stk, nil, &fullAddr, ipv6.ProtocolNumber)
			if err != nil {
				return nil, err
			}
			return c, nil
		default:
			return nil, fmt.Errorf("netstack dialer: unsupported network %q", network)
		}
	}
}
