package qmidatapath

import "context"

// PacketSink is the host-side endpoint of the relay (the non-USB side).
// It exchanges raw IP packets with the relay Bridge.
//
// Implementations:
//   - TUNPacketSink (wraps wireguard/tun.Device)
//   - NetstackPacketSink (wraps gVisor channel link endpoint)
//
// Contract:
//   - ReadPacket blocks until a packet is available or ctx is canceled.
//   - Close MUST unblock any pending ReadPacket (return ctx.Err() or io.EOF).
//     Otherwise Bridge.Stop() (which calls wg.Wait) will deadlock.
type PacketSink interface {
	// ReadPacket reads one raw IP packet (host → modem / uplink).
	// pkt is valid until the next ReadPacket call.
	// Returns io.EOF (or ctx.Err()) when the sink is closed.
	ReadPacket(ctx context.Context) (pkt []byte, err error)

	// WritePacket writes one raw IP packet (modem → host / downlink).
	// pkt is a bare IP packet (no TUN prefix, no QMAP header).
	WritePacket(pkt []byte) error

	// Name returns the sink's identifier for logging (e.g. "qmi0", "netstack").
	Name() string

	// Close releases the sink's resources. Must unblock pending ReadPacket.
	Close() error
}
