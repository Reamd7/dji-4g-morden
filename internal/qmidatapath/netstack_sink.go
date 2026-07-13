package qmidatapath

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

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

	// DNS servers for resolution over the 4G link (set via SetDNSServers).
	// When empty, NetstackDialer falls back to the host resolver.
	dnsServers   []netip.Addr
	resolverOnce sync.Once
	resolver     *net.Resolver
	resolverErr  error
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
func (s *NetstackPacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
	pkt := s.ep.ReadContext(ctx)
	if pkt == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("netstack: channel closed")
	}
	defer pkt.DecRef()
	// ToView() returns the FULL packet including all headers (network + transport + data).
	view := pkt.ToView()
	return view.AsSlice(), nil
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

func (s *NetstackPacketSink) Close() error {
	s.ep.Close()
	s.stk.Close()
	return nil
}

// SetDNSServers configures DNS servers for resolution over the 4G link.
// When set, NetstackDialer resolves hostnames via these servers through the
// netstack (DNS queries travel USB → modem → 4G) instead of the host
// resolver. Pass modem-assigned DNS (mgr.Settings().IPv4DNS1/DNS2).
func (s *NetstackPacketSink) SetDNSServers(servers []netip.Addr) {
	s.dnsServers = append(s.dnsServers[:0:0], servers...)
	s.resolverOnce = sync.Once{} // invalidate cached resolver
	s.resolver = nil
	s.resolverErr = nil
}

// resolver4G builds (once) a *net.Resolver whose Dial hook routes DNS
// queries through the gVisor netstack → USB → modem → 4G, targeting the
// first configured DNS server. DNS protocol handling (compression pointers,
// CNAME, TCP fallback) is delegated to the standard library.
func (s *NetstackPacketSink) resolver4G() (*net.Resolver, error) {
	s.resolverOnce.Do(func() {
		if len(s.dnsServers) == 0 {
			s.resolverErr = errors.New("netstack: no DNS servers configured")
			return
		}
		dns := s.dnsServers[0]
		proto := ipv4.ProtocolNumber
		if dns.Is6() {
			proto = ipv6.ProtocolNumber
		}
		dnsAddr := tcpip.AddrFromSlice(dns.AsSlice())
		s.resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				fullAddr := tcpip.FullAddress{NIC: s.nicID, Addr: dnsAddr, Port: 53}
				if strings.HasPrefix(network, "tcp") {
					return gonet.DialContextTCP(ctx, s.stk, fullAddr, proto)
				}
				return gonet.DialUDP(s.stk, nil, &fullAddr, proto)
			},
		}
		s.resolverErr = nil
	})
	return s.resolver, s.resolverErr
}

// resolve looks up host's IP addresses. With 4G DNS servers configured, queries
// go over the netstack → 4G link; otherwise the host resolver is used.
func (s *NetstackPacketSink) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if len(s.dnsServers) == 0 {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		return toNetipAddrs(ips), nil
	}
	r, err := s.resolver4G()
	if err != nil {
		return nil, err
	}
	ips, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	return toNetipAddrs(ips), nil
}

func toNetipAddrs(ips []net.IPAddr) []netip.Addr {
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if a, ok := netip.AddrFromSlice(ip.IP); ok {
			out = append(out, a.Unmap())
		}
	}
	return out
}

// pickIP selects an IP matching the network's address family (v4/v6).
// For unspecified families ("tcp"/"udp") the first IP is returned.
func pickIP(ips []netip.Addr, network string) netip.Addr {
	if len(ips) == 0 {
		return netip.Addr{}
	}
	switch {
	case strings.HasSuffix(network, "6"):
		for _, ip := range ips {
			if ip.Is6() {
				return ip
			}
		}
	case strings.HasSuffix(network, "4"):
		for _, ip := range ips {
			if ip.Is4() {
				return ip
			}
		}
	}
	return ips[0]
}

// NetstackDialer returns a Dial function suitable for SOCKS5 server's
// Config.Dial. Connections dialed through this function go through the
// gVisor netstack → channel → USB → modem (NOT the host network).
//
// DNS resolution (Phase 3): when SetDNSServers has been called, DNS queries
// are issued over the netstack → 4G link (via a net.Resolver whose Dial hook
// routes through gonet). With no DNS servers configured, it falls back to
// the host resolver.
func (s *NetstackPacketSink) NetstackDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("netstack dialer: bad addr %q: %w", addr, err)
		}
		portNum, err := net.LookupPort("tcp", port)
		if err != nil {
			return nil, fmt.Errorf("netstack dialer: bad port %q: %w", port, err)
		}

		ips, err := s.resolve(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("netstack dialer: resolve %q: %w", host, err)
		}
		ipAddr := pickIP(ips, network)
		if !ipAddr.IsValid() {
			return nil, fmt.Errorf("netstack dialer: no IP for %s", host)
		}
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
