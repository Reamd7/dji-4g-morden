// Package qmidatapath implements a bidirectional raw-IP relay between a USB
// bulk endpoint pair (QMI data channel) and a TUN virtual network interface.
//
// After WDA SetDataFormat(LinkProtocolIP) + WDS StartNetwork (stage 2),
// the modem sends/receives raw IP packets (no ethernet header, no QMAP
// wrapper) on MI_04's bulk IN (EP 0x88) / bulk OUT (EP 0x05). The TUN
// device (wireguard/tun) is also layer-3 (raw IP). Both sides agree on
// framing, so the relay is a direct byte-for-byte forward.
//
//   ┌──────────────┐   raw IP   ┌──────────────────┐   raw IP   ┌──────────────┐
//   │ Host network │ ─────────▶ │  TUN Device      │ ─────────▶ │ Modem USB    │
//   │  stack       │  TUN.Read  │  (wireguard/tun) │  bulk OUT  │ EP 0x05 OUT  │
//   │              │ ◀───────── │                  │ ◀───────── │ EP 0x88 IN   │
//   │              │  TUN.Write │                  │  bulk IN   │              │
//   └──────────────┘            └──────────────────┘            └──────────────┘
//
// The relay has two goroutines:
//
//   - tunToModem: TUN.Read → bulkOut.Write (+ optional ZLP for 512-multiple)
//   - modemToTun: bulkIn.ReadContext → TUN.Write
//
// ZLP (Zero Length Packet): the modem's bulk OUT endpoint has maxPacketSize=512.
// When a TX packet's length is an exact multiple of 512, the modem may buffer
// it waiting for more data. Linux's qmi_wwan_q.c sets FLAG_SEND_ZLP to send a
// trailing zero-length URB. Our probe (subplan 00 D2) confirmed this: 28B
// packets get replies, 512B packets do not without ZLP. When zlp=true, the
// relay appends a 0-byte Write after any packet whose length % 512 == 0.
package qmidatapath

import (
	"context"
	"log"
	"sync"
)

// BulkReader abstracts the gousb IN endpoint (EP 0x88) so relay logic can be
// unit-tested with a mock. gousb *InEndpoint satisfies this.
type BulkReader interface {
	ReadContext(ctx context.Context, buf []byte) (int, error)
}

// BulkWriter abstracts the gousb OUT endpoint (EP 0x05).
// gousb *OutEndpoint satisfies this.
type BulkWriter interface {
	Write(buf []byte) (int, error)
}

// tunDevice abstracts wireguard/tun.Device for testing.
// wireguard/tun.Device satisfies this (it has all these methods and more).
type tunDevice interface {
	Read(bufs [][]byte, sizes []int, offset int) (int, error)
	Write(bufs [][]byte, offset int) (int, error)
	Name() (string, error)
	Close() error
	BatchSize() int
}

// USB 2.0 high-speed bulk endpoint maxPacketSize.
const bulkMaxPacketSize = 512

// Bridge relays raw IP packets between a USB bulk endpoint pair and a TUN
// device. Both sides are layer-3 (no ethernet header), so packets pass
// through unchanged.
type Bridge struct {
	tun     tunDevice
	bulkIn  BulkReader
	bulkOut BulkWriter
	offset  int // macOS=4 (utun AF-family headroom), others=0
	mtu     int // typically 1500
	zlp     bool // append ZLP after 512-multiple TX packets

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool
}

// New creates a Bridge. tun/bulkIn/bulkOut must be pre-opened by the caller.
// offset: runtime.GOOS=="darwin" ? 4 : 0.
// zlp: true if the modem needs ZLP (confirmed by subplan 00 probe — true for QDC507).
func New(tun tunDevice, bulkIn BulkReader, bulkOut BulkWriter, offset, mtu int, zlp bool) *Bridge {
	return &Bridge{
		tun:     tun,
		bulkIn:  bulkIn,
		bulkOut: bulkOut,
		offset:  offset,
		mtu:     mtu,
		zlp:     zlp,
	}
}

// Start launches the two relay goroutines (TUN→modem, modem→TUN).
// Idempotent — calling twice is a no-op.
func (b *Bridge) Start(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return nil
	}

	ctx, cancel := context.WithCancel(parent)
	b.cancel = cancel
	b.started = true

	b.wg.Add(2)
	go b.tunToModem(ctx)
	go b.modemToTun(ctx)

	return nil
}

// Stop cancels the relay context and waits for both goroutines to exit.
// Does NOT close the TUN — the caller owns TUN lifecycle.
// Safe to call after Start; safe to call multiple times.
func (b *Bridge) Stop() {
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		return
	}
	b.started = false
	b.mu.Unlock()

	if b.cancel != nil {
		b.cancel()
	}
	b.wg.Wait()
}

// Flow 1: TUN → modem (uplink).
// Reads IP packets from the TUN device and writes them to the USB bulk OUT
// endpoint. When zlp is enabled, appends a Zero Length Packet after any
// packet whose length is an exact multiple of the bulk endpoint's
// maxPacketSize (512 bytes).
func (b *Bridge) tunToModem(ctx context.Context) {
	defer b.wg.Done()

	batchSize := b.tun.BatchSize()
	if batchSize < 1 {
		batchSize = 1
	}
	bufs := make([][]byte, batchSize)
	sizes := make([]int, batchSize)
	for i := range bufs {
		bufs[i] = make([]byte, b.mtu+b.offset)
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := b.tun.Read(bufs, sizes, b.offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("qmidatapath: TUN read error: %v", err)
			continue
		}

		for i := 0; i < n; i++ {
			pkt := bufs[i][b.offset : b.offset+sizes[i]]
			if _, err := b.bulkOut.Write(pkt); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("qmidatapath: bulk OUT write error: %v", err)
				continue
			}
			// R5 ZLP: if pkt length is a multiple of bulk OUT maxPacketSize,
			// the modem may buffer it waiting for more data. Append a 0-byte
			// write (Zero Length Packet) to signal end-of-transfer.
			if b.zlp && len(pkt)%bulkMaxPacketSize == 0 {
				b.bulkOut.Write([]byte{})
			}
		}
	}
}

// Flow 2: modem → TUN (downlink).
// Reads raw IP from the USB bulk IN endpoint and writes it to the TUN device.
// A large buffer (65535) is used so a single ReadContext call can receive
// any IP packet up to the maximum size.
func (b *Bridge) modemToTun(ctx context.Context) {
	defer b.wg.Done()

	buf := make([]byte, 65535)
	outBuf := make([]byte, b.mtu+b.offset)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := b.bulkIn.ReadContext(ctx, buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("qmidatapath: bulk IN read error: %v", err)
			continue
		}
		if n == 0 {
			continue
		}

		// raw-IP: buf[:n] is a bare IP packet. Copy into outBuf with offset
		// headroom for the TUN's protocol prefix (macOS utun needs 4 bytes).
		copy(outBuf[b.offset:], buf[:n])
		if _, err := b.tun.Write([][]byte{outBuf[:b.offset+n]}, b.offset); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("qmidatapath: TUN write error: %v", err)
		}
	}
}
