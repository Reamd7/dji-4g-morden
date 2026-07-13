// Package qmidatapath implements a bidirectional raw-IP relay between a USB
// bulk endpoint pair (QMI data channel) and a PacketSink (TUN device or
// gVisor netstack channel).
//
// After WDA SetDataFormat(LinkProtocolIP) + WDS StartNetwork (stage 2),
// the modem sends/receives raw IP packets (no ethernet header, no QMAP
// wrapper) on MI_04's bulk IN (EP 0x88) / bulk OUT (EP 0x05). Both TUN
// and netstack channel are layer-3 (raw IP). All sides agree on framing,
// so the relay is a direct byte-for-byte forward.
//
//   ┌──────────────┐   raw IP   ┌──────────────────┐   raw IP   ┌──────────────┐
//   │ Host network │ ─────────▶ │  PacketSink      │ ─────────▶ │ Modem USB    │
//   │  stack       │  ReadPacket│  (TUN/netstack)  │  bulk OUT  │ EP 0x05 OUT  │
//   │              │ ◀───────── │                  │ ◀───────── │ EP 0x88 IN   │
//   │              │ WritePacket│                  │  bulk IN   │              │
//   └──────────────┘            └──────────────────┘            └──────────────┘
//
// The relay has two goroutines:
//
//   - sinkToModem: sink.ReadPacket → bulkOut.Write (+ optional ZLP for 512-multiple)
//   - modemToSink: bulkIn.ReadContext → sink.WritePacket
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
	"sync/atomic"
	"time"
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

// USB 2.0 high-speed bulk endpoint maxPacketSize.
const bulkMaxPacketSize = 512

// Bridge relays raw IP packets between a USB bulk endpoint pair and a
// PacketSink. Both sides are layer-3 (no ethernet header), so packets pass
// through unchanged.
type Bridge struct {
	sink    PacketSink
	bulkIn  BulkReader
	bulkOut BulkWriter
	mtu     int
	zlp     bool // append ZLP after 512-multiple TX packets
	microBatch bool          // coalesce uplink writes (default off; experimental)
	batchSize  int           // max packets per coalesced write
	batchDelay time.Duration // max wait before flushing a partial batch

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool

	// Packet counters (atomic, for debugging)
	txPackets atomic.Int64 // sink → modem (uplink)
	txBytes   atomic.Int64
	rxPackets atomic.Int64 // modem → sink (downlink)
	rxBytes   atomic.Int64
}

// Stats returns relay packet counters.
func (b *Bridge) Stats() (txPkt, txByt, rxPkt, rxByt int64) {
	return b.txPackets.Load(), b.txBytes.Load(), b.rxPackets.Load(), b.rxBytes.Load()
}

// New creates a Bridge. sink/bulkIn/bulkOut must be pre-opened by the caller.
// zlp: true if the modem needs ZLP (confirmed by subplan 00 probe — true for QDC507).
func New(sink PacketSink, bulkIn BulkReader, bulkOut BulkWriter, mtu int, zlp bool) *Bridge {
	return &Bridge{
		sink:    sink,
		bulkIn:  bulkIn,
		bulkOut: bulkOut,
		mtu:     mtu,
		zlp:     zlp,
	}
}

// SetMicroBatching enables uplink micro-batching: accumulate up to size
// packets (or delay) into a single bulk OUT write to reduce USB write
// frequency. Experimental — modem compatibility with coalesced raw-IP writes
// is unverified (raw-IP packets carry their own length, so the modem should
// split on IP boundaries, but not hardware-tested). Leave off unless
// benchmarked. Must be called before Start.
func (b *Bridge) SetMicroBatching(size int, delay time.Duration) {
	b.microBatch = size > 0
	b.batchSize = size
	b.batchDelay = delay
}

// Start launches the two relay goroutines (sink→modem, modem→sink).
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
	go b.sinkToModem(ctx)
	go b.modemToSink(ctx)

	return nil
}

// Stop cancels the relay context and waits for both goroutines to exit.
// Does NOT close the sink — the caller owns sink lifecycle.
// Safe to call after Start; safe to call multiple times.
//
// NOTE: For TUN-backed sinks, the caller MUST close the sink before calling
// Stop (or simultaneously), because TUNPacketSink.ReadPacket issues a read
// that does not honor context cancellation. Closing the sink unblocks the
// read so the goroutine exits and Stop's wg.Wait() returns. Netstack-backed
// sinks don't deadlock here (channel close → ReadContext returns nil), but
// sink.Close() before Stop is still the recommended order.
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
	// Wait with timeout — USB ReadContext on some platforms may not
	// immediately honor context cancellation (WinUSB). 5s is generous;
	// if goroutines haven't exited by then, something is stuck.
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("qmidatapath: Bridge.Stop() timed out after 5s — goroutine may be stuck on USB read")
	}
}

// Flow 1: sink → modem (uplink).
// Reads IP packets from the PacketSink and writes them to the USB bulk OUT
// endpoint. When zlp is enabled, appends a Zero Length Packet after any
// packet whose length is an exact multiple of the bulk endpoint's
// maxPacketSize (512 bytes).
func (b *Bridge) sinkToModem(ctx context.Context) {
	defer b.wg.Done()
	if b.microBatch {
		b.sinkToModemBatch(ctx)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pkt, err := b.sink.ReadPacket(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("qmidatapath: sink read error: %v", err)
			continue
		}
		if len(pkt) == 0 {
			continue
		}

		b.txPackets.Add(1)
		b.txBytes.Add(int64(len(pkt)))
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

// sinkToModemBatch coalesces uplink packets into fewer bulk OUT writes. A
// reader goroutine feeds ReadPacket results into a channel; the main loop
// accumulates until batchSize packets or batchDelay elapses, then issues a
// single Write. ZLP (if enabled) is appended when the coalesced buffer
// length is a multiple of 512.
func (b *Bridge) sinkToModemBatch(ctx context.Context) {
	pkts := make(chan []byte, b.batchSize)
	go func() {
		for {
			pkt, err := b.sink.ReadPacket(ctx)
			if err != nil {
				return
			}
			select {
			case pkts <- pkt:
			case <-ctx.Done():
				return
			}
		}
	}()

	buf := make([]byte, 0, b.batchSize*1500)
	count := 0
	timer := time.NewTimer(b.batchDelay)
	defer timer.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		if _, err := b.bulkOut.Write(buf); err != nil {
			if ctx.Err() == nil {
				log.Printf("qmidatapath: bulk OUT batch write error: %v", err)
			}
		} else if b.zlp && len(buf)%bulkMaxPacketSize == 0 {
			b.bulkOut.Write([]byte{})
		}
		buf = buf[:0]
		count = 0
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case pkt := <-pkts:
			if len(pkt) == 0 {
				continue
			}
			buf = append(buf, pkt...)
			b.txPackets.Add(1)
			b.txBytes.Add(int64(len(pkt)))
			count++
			if count >= b.batchSize {
				flush()
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(b.batchDelay)
			}
		case <-timer.C:
			flush()
			timer.Reset(b.batchDelay)
		}
	}
}

// Flow 2: modem → sink (downlink).
// Reads raw IP from the USB bulk IN endpoint and writes it to the PacketSink.
// A large buffer (65535) is used so a single ReadContext call can receive
// any IP packet up to the maximum size.
func (b *Bridge) modemToSink(ctx context.Context) {
	defer b.wg.Done()

	buf := make([]byte, 65535)

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
		b.rxPackets.Add(1)
		b.rxBytes.Add(int64(n))

		if err := b.sink.WritePacket(buf[:n]); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("qmidatapath: sink write error: %v", err)
		}
	}
}
