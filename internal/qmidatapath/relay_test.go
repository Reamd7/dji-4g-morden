package qmidatapath

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ── Fake implementations ──────────────────────────────────────────────────

// fakeBulkReader simulates gousb InEndpoint. Push packets via the pkts channel.
type fakeBulkReader struct {
	pkts chan []byte
}

func newFakeBulkReader() *fakeBulkReader {
	return &fakeBulkReader{pkts: make(chan []byte, 16)}
}

func (f *fakeBulkReader) ReadContext(ctx context.Context, buf []byte) (int, error) {
	select {
	case p := <-f.pkts:
		return copy(buf, p), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// fakeBulkWriter records all written packets.
type fakeBulkWriter struct {
	mu      sync.Mutex
	written [][]byte
}

func (f *fakeBulkWriter) Write(buf []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, append([]byte(nil), buf...))
	return len(buf), nil
}

func (f *fakeBulkWriter) packets() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.written))
	copy(out, f.written)
	return out
}

// fakePacketSink implements PacketSink for testing.
// ReadPacket pops from rx (host → modem). WritePacket pushes to tx (modem → host).
type fakePacketSink struct {
	rx       chan []byte // ReadPacket pops from here
	tx       chan []byte // WritePacket pushes here
	done     chan struct{}
	closeOnce sync.Once
}

func newFakePacketSink() *fakePacketSink {
	return &fakePacketSink{
		rx:   make(chan []byte, 16),
		tx:   make(chan []byte, 16),
		done: make(chan struct{}),
	}
}

func (f *fakePacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
	select {
	case p := <-f.rx:
		return p, nil
	case <-f.done:
		return nil, fmt.Errorf("sink closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakePacketSink) WritePacket(pkt []byte) error {
	select {
	case f.tx <- append([]byte(nil), pkt...):
		return nil
	case <-f.done:
		return fmt.Errorf("sink closed")
	}
}

func (f *fakePacketSink) Name() string { return "fake-sink" }

func (f *fakePacketSink) Close() error {
	f.closeOnce.Do(func() { close(f.done) })
	return nil
}

// stopBridgeAndSink is a test helper that closes the sink and stops the bridge.
func stopBridgeAndSink(bridge *Bridge, sink *fakePacketSink) {
	sink.Close()
	bridge.Stop()
}

// ── Tests ──────────────────────────────────────────────────────────────────

// TestRelayModemToSink verifies that packets from bulk IN are forwarded to sink.
func TestRelayModemToSink(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push an IPv4 packet into bulk IN.
	ipv4Pkt := makeIPv4Packet(64)
	bulkIn.pkts <- ipv4Pkt

	select {
	case got := <-sink.tx:
		if len(got) != len(ipv4Pkt) {
			t.Fatalf("sink got %d bytes, want %d", len(got), len(ipv4Pkt))
		}
		if got[0]>>4 != 4 {
			t.Fatalf("sink got version=%d, want 4", got[0]>>4)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sink did not receive packet within 2s")
	}

	cancel()
	stopBridgeAndSink(bridge, sink)
}

// TestRelaySinkToModem verifies that packets from sink are forwarded to bulk OUT.
func TestRelaySinkToModem(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push an IPv4 packet into sink.
	ipv4Pkt := makeIPv4Packet(64)
	sink.rx <- ipv4Pkt

	// Wait for bulk OUT to receive it.
	deadline := time.After(2 * time.Second)
	for {
		pkts := bulkOut.packets()
		if len(pkts) >= 1 {
			if len(pkts[0]) != len(ipv4Pkt) {
				t.Fatalf("bulk OUT got %d bytes, want %d", len(pkts[0]), len(ipv4Pkt))
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("bulk OUT did not receive packet within 2s")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	stopBridgeAndSink(bridge, sink)
}

// TestRelayRawIPPassthrough verifies that raw IP bytes pass through unchanged.
func TestRelayRawIPPassthrough(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push a packet with recognizable content.
	pkt := makeIPv4Packet(32)
	pkt[20] = 0xAB // Mark the ICMP payload
	bulkIn.pkts <- pkt

	select {
	case got := <-sink.tx:
		if got[20] != 0xAB {
			t.Fatalf("payload byte changed: got 0x%02x, want 0xAB", got[20])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sink did not receive packet")
	}

	cancel()
	stopBridgeAndSink(bridge, sink)
}

// TestRelayZLP verifies that 512-multiple TX packets trigger a ZLP write
// when zlp=true, and do NOT when zlp=false.
func TestRelayZLP(t *testing.T) {
	t.Run("ZLPEnabled", func(t *testing.T) {
		bulkIn := newFakeBulkReader()
		bulkOut := &fakeBulkWriter{}
		sink := newFakePacketSink()

		bridge := New(sink, bulkIn, bulkOut, 1500, true)
		ctx, cancel := context.WithCancel(context.Background())
		bridge.Start(ctx)

		// Send a 512-byte packet via sink (512 = 1 × maxPacketSize).
		pkt512 := makeIPv4Packet(512)
		if len(pkt512) != 512 {
			t.Fatalf("test packet is %d bytes, want 512", len(pkt512))
		}
		sink.rx <- pkt512

		// Wait for 2 writes: the packet + the ZLP.
		deadline := time.After(2 * time.Second)
		for {
			pkts := bulkOut.packets()
			if len(pkts) >= 2 {
				if len(pkts[0]) != 512 {
					t.Fatalf("first write: %d bytes, want 512", len(pkts[0]))
				}
				if len(pkts[1]) != 0 {
					t.Fatalf("ZLP write: %d bytes, want 0", len(pkts[1]))
				}
				break
			}
			select {
			case <-deadline:
				t.Fatalf("expected 2 writes (packet+ZLP), got %d", len(bulkOut.packets()))
			case <-time.After(10 * time.Millisecond):
			}
		}

		cancel()
		stopBridgeAndSink(bridge, sink)
	})

	t.Run("ZLPDisabled", func(t *testing.T) {
		bulkIn := newFakeBulkReader()
		bulkOut := &fakeBulkWriter{}
		sink := newFakePacketSink()

		bridge := New(sink, bulkIn, bulkOut, 1500, false)
		ctx, cancel := context.WithCancel(context.Background())
		bridge.Start(ctx)

		pkt512 := makeIPv4Packet(512)
		sink.rx <- pkt512

		// Wait for 1 write only (no ZLP).
		deadline := time.After(2 * time.Second)
		for {
			pkts := bulkOut.packets()
			if len(pkts) >= 1 {
				time.Sleep(100 * time.Millisecond)
				pkts = bulkOut.packets()
				if len(pkts) != 1 {
					t.Fatalf("expected 1 write (no ZLP), got %d", len(pkts))
				}
				break
			}
			select {
			case <-deadline:
				t.Fatal("bulk OUT did not receive packet")
			case <-time.After(10 * time.Millisecond):
			}
		}

		cancel()
		stopBridgeAndSink(bridge, sink)
	})
}

// TestBridgeStopWaitsGoroutines verifies that Stop blocks until goroutines exit.
func TestBridgeStopWaitsGoroutines(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Close the sink to unblock sinkToModem, then Stop.
	sink.Close()
	cancel()

	done := make(chan struct{})
	go func() {
		bridge.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Bridge.Stop() did not return within 3s — goroutine leak")
	}
}

// TestBridgeCloseOrdering verifies the close ordering for sinks that
// don't respect context in ReadPacket (like TUNPacketSink).
// For fakePacketSink, ReadPacket respects ctx, so cancel alone suffices.
func TestBridgeCloseOrdering(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Cancel context → close sink → bridge.Stop
	cancel()
	sink.Close()

	done := make(chan struct{})
	go func() {
		bridge.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop deadlocked")
	}
}

// TestRelayContextCancel verifies that cancelling the context stops both
// goroutines cleanly.
func TestRelayContextCancel(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	cancel()
	sink.Close() // unblock sinkToModem

	done := make(chan struct{})
	go func() {
		bridge.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Bridge did not stop after context cancel + sink close")
	}
}

// TestConcurrentRelayRace exercises the relay under -race.
func TestConcurrentRelayRace(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, true)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			pkt := makeIPv4Packet(64 + i)
			select {
			case bulkIn.pkts <- pkt:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			pkt := makeIPv4Packet(128 + i)
			select {
			case sink.rx <- pkt:
			case <-ctx.Done():
				return
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	sink.Close()
	bridge.Stop()
	wg.Wait()
}

// TestRelayStats verifies packet counters after relay.
func TestRelayStats(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push 3 TX packets
	for i := 0; i < 3; i++ {
		sink.rx <- makeIPv4Packet(64)
	}
	// Push 2 RX packets
	for i := 0; i < 2; i++ {
		bulkIn.pkts <- makeIPv4Packet(64)
	}

	time.Sleep(100 * time.Millisecond)

	txPkt, txByt, rxPkt, rxByt := bridge.Stats()
	if txPkt < 3 {
		t.Errorf("TX packets=%d, want >=3", txPkt)
	}
	if rxPkt < 2 {
		t.Errorf("RX packets=%d, want >=2", rxPkt)
	}
	if txByt <= 0 || rxByt <= 0 {
		t.Errorf("bytes: TX=%d RX=%d, both should be >0", txByt, rxByt)
	}

	cancel()
	sink.Close()
	bridge.Stop()
}

// TestStartIdempotent verifies Start can be called twice without panic.
func TestStartIdempotent(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	sink.Close()
	bridge.Stop()
}

// TestStopWithoutStart verifies Stop is safe before Start.
func TestStopWithoutStart(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	bridge.Stop() // should be a no-op, no panic
}

// TestRelayBulkWriteError verifies the relay continues when bulkOut.Write fails.
func TestRelayBulkWriteError(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &errorBulkWriter{}
	sink := newFakePacketSink()

	bridge := New(sink, bulkIn, bulkOut, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push a packet — bulkOut.Write will fail, relay should log and continue
	sink.rx <- makeIPv4Packet(64)
	time.Sleep(100 * time.Millisecond)

	cancel()
	sink.Close()
	bridge.Stop()

	// Verify relay didn't crash (Stats accessible)
	txPkt, _, _, _ := bridge.Stats()
	if txPkt < 1 {
		t.Errorf("TX packets=%d, expected >=1 (counted before write error)", txPkt)
	}
}

// errorBulkWriter always returns an error on Write.
type errorBulkWriter struct{}

func (e *errorBulkWriter) Write(buf []byte) (int, error) {
	return 0, fmt.Errorf("simulated write error")
}

// ── Helpers ────────────────────────────────────────────────────────────────

// makeIPv4Packet creates a minimal valid-ish IPv4 packet of the given total
// length (including 20-byte IP header). If totalLen < 20, returns 20 bytes.
func makeIPv4Packet(totalLen int) []byte {
	if totalLen < 20 {
		totalLen = 20
	}
	pkt := make([]byte, totalLen)
	pkt[0] = 0x45 // version=4, IHL=5
	pkt[1] = 0x00
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	pkt[8] = 64 // TTL
	pkt[9] = 1  // ICMP
	return pkt
}

// TestBridgeMicroBatchCoalesces verifies micro-batching coalesces multiple
// uplink packets into a single bulk OUT write when batchSize is reached.
func TestBridgeMicroBatchCoalesces(t *testing.T) {
	sink := newFakePacketSink()
	defer sink.Close()
	w := &fakeBulkWriter{}
	br := newFakeBulkReader()

	b := New(sink, br, w, 1500, false)
	b.SetMicroBatching(3, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()

	for i := range 3 {
		sink.rx <- []byte{0x45, 0x00, 0x00, 0x14, byte(i)}
	}
	time.Sleep(50 * time.Millisecond)

	got := w.packets()
	if len(got) != 1 {
		t.Fatalf("micro-batch: want 1 coalesced write, got %d", len(got))
	}
	if len(got[0]) != 15 {
		t.Fatalf("coalesced write len = %d, want 15", len(got[0]))
	}
}

// TestBridgeMicroBatchTimeoutFlush verifies a partial batch flushes on timeout.
func TestBridgeMicroBatchTimeoutFlush(t *testing.T) {
	sink := newFakePacketSink()
	defer sink.Close()
	w := &fakeBulkWriter{}
	br := newFakeBulkReader()

	b := New(sink, br, w, 1500, false)
	b.SetMicroBatching(16, 30*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()

	sink.rx <- []byte{0x45, 0x00, 0x00, 0x14}
	time.Sleep(80 * time.Millisecond)

	got := w.packets()
	if len(got) != 1 {
		t.Fatalf("timeout flush: want 1 write, got %d", len(got))
	}
	if len(got[0]) != 4 {
		t.Fatalf("flushed write len = %d, want 4", len(got[0]))
	}
}

// TestBridgeMicroBatchOffDefaultsSingle verifies that without SetMicroBatching,
// each packet is written individually (default behavior preserved).
func TestBridgeMicroBatchOffDefaultsSingle(t *testing.T) {
	sink := newFakePacketSink()
	defer sink.Close()
	w := &fakeBulkWriter{}
	br := newFakeBulkReader()

	b := New(sink, br, w, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()

	for i := range 3 {
		sink.rx <- []byte{0x45, 0x00, 0x00, 0x14, byte(i)}
	}
	time.Sleep(50 * time.Millisecond)

	got := w.packets()
	if len(got) != 3 {
		t.Fatalf("default (no micro-batch): want 3 writes, got %d", len(got))
	}
}
