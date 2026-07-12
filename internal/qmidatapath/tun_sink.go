package qmidatapath

import (
	"context"

	"golang.zx2c4.com/wireguard/tun"
)

// TUNPacketSink wraps a wireguard/tun.Device as a PacketSink.
// It handles the TUN-specific offset (macOS utun 4-byte protocol-family prefix)
// internally so the Bridge is offset-agnostic.
//
// Note: this drops the TUN batch-read optimization (BatchSize() up to N packets
// per Read call). TUNPacketSink reads one packet at a time. This is a no-op on
// Windows (BatchSize()==1 there anyway) and negligible on Linux/macOS at 4G
// bandwidths.
type TUNPacketSink struct {
	dev    tun.Device
	offset int // macOS=4, others=0
	mtu    int

	// Single-buffer reuse — Read/Write are single-threaded within the Bridge
	// (sinkToModem reads, modemToSink writes, different goroutines but never
	// concurrent on the same buffer).
	readBuf  []byte
	writeBuf []byte
}

// NewTUNPacketSink creates a TUN-backed PacketSink.
// offset: runtime.GOOS=="darwin" ? 4 : 0.
func NewTUNPacketSink(dev tun.Device, offset, mtu int) *TUNPacketSink {
	return &TUNPacketSink{
		dev:      dev,
		offset:   offset,
		mtu:      mtu,
		readBuf:  make([]byte, mtu+offset),
		writeBuf: make([]byte, mtu+offset),
	}
}

// ReadPacket reads one packet from the TUN device (host → modem / uplink).
// Blocks until a packet is available or the TUN is closed.
//
// NOTE: tun.Read does NOT honor context — it blocks until the TUN is closed.
// This is the known TUN limitation. Bridge.Stop() works around it by requiring
// the caller to Close the TUN first, which unblocks Read.
func (s *TUNPacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
	bufs := [][]byte{s.readBuf}
	sizes := make([]int, 1)
	n, err := s.dev.Read(bufs, sizes, s.offset)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return s.readBuf[s.offset : s.offset+sizes[0]], nil
}

// WritePacket writes one packet to the TUN device (modem → host / downlink).
func (s *TUNPacketSink) WritePacket(pkt []byte) error {
	copy(s.writeBuf[s.offset:], pkt)
	_, err := s.dev.Write([][]byte{s.writeBuf[:s.offset+len(pkt)]}, s.offset)
	return err
}

func (s *TUNPacketSink) Name() string {
	name, err := s.dev.Name()
	if err != nil {
		return "tun(?)"
	}
	return name
}

func (s *TUNPacketSink) Close() error {
	return s.dev.Close()
}
