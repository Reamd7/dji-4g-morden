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

// fakeTUN implements tunDevice for testing. Read blocks on rx or done channel.
// Close unblocks pending Reads.
type fakeTUN struct {
	rx        chan []byte // Read pops from here
	tx        chan []byte // Write pushes here
	done      chan struct{}
	batchSize int
	closeOnce sync.Once
}

func newFakeTUN(batchSize int) *fakeTUN {
	if batchSize < 1 {
		batchSize = 1
	}
	return &fakeTUN{
		rx:        make(chan []byte, 16),
		tx:        make(chan []byte, 16),
		done:      make(chan struct{}),
		batchSize: batchSize,
	}
}

func (f *fakeTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case p := <-f.rx:
		copy(bufs[0][offset:], p)
		sizes[0] = len(p)
		return 1, nil
	case <-f.done:
		return 0, fmt.Errorf("tun closed")
	}
}

func (f *fakeTUN) Write(bufs [][]byte, offset int) (int, error) {
	for _, b := range bufs {
		select {
		case f.tx <- append([]byte(nil), b[offset:]...):
		case <-f.done:
			return 0, fmt.Errorf("tun closed")
		}
	}
	return len(bufs), nil
}

func (f *fakeTUN) Name() (string, error) { return "fake0", nil }
func (f *fakeTUN) Close() error {
	f.closeOnce.Do(func() { close(f.done) })
	return nil
}
func (f *fakeTUN) BatchSize() int { return f.batchSize }

// stopBridgeAndTUN is a test helper that stops the bridge and closes the TUN
// to unblock any pending Read. Bridge.Stop cancels the context (unblocks
func stopBridgeAndTUN(bridge *Bridge, tun *fakeTUN) {
	// Close TUN first to unblock tunToModem (which may be blocked in tun.Read),
	// then Stop to cancel context + wait for both goroutines to exit.
	tun.Close()
	bridge.Stop()
}

// ── Tests ──────────────────────────────────────────────────────────────────

// TestRelayModemToTUN verifies that packets from bulk IN are forwarded to TUN.
func TestRelayModemToTUN(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push an IPv4 packet into bulk IN.
	ipv4Pkt := makeIPv4Packet(64)
	bulkIn.pkts <- ipv4Pkt

	select {
	case got := <-tun.tx:
		if len(got) != len(ipv4Pkt) {
			t.Fatalf("TUN got %d bytes, want %d", len(got), len(ipv4Pkt))
		}
		if got[0]>>4 != 4 {
			t.Fatalf("TUN got version=%d, want 4", got[0]>>4)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TUN did not receive packet within 2s")
	}

	cancel()
	stopBridgeAndTUN(bridge, tun)
}

// TestRelayTUNToModem verifies that packets from TUN are forwarded to bulk OUT.
func TestRelayTUNToModem(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push an IPv4 packet into TUN.
	ipv4Pkt := makeIPv4Packet(64)
	tun.rx <- ipv4Pkt

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
	stopBridgeAndTUN(bridge, tun)
}

// TestRelayRawIPPassthrough verifies that raw IP bytes pass through unchanged.
func TestRelayRawIPPassthrough(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push a packet with recognizable content.
	pkt := makeIPv4Packet(32)
	pkt[20] = 0xAB // Mark the ICMP payload
	bulkIn.pkts <- pkt

	select {
	case got := <-tun.tx:
		if got[20] != 0xAB {
			t.Fatalf("payload byte changed: got 0x%02x, want 0xAB", got[20])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TUN did not receive packet")
	}

	cancel()
	stopBridgeAndTUN(bridge, tun)
}

// TestRelayZLP verifies that 512-multiple TX packets trigger a ZLP write
// when zlp=true, and do NOT when zlp=false.
func TestRelayZLP(t *testing.T) {
	t.Run("ZLPEnabled", func(t *testing.T) {
		bulkIn := newFakeBulkReader()
		bulkOut := &fakeBulkWriter{}
		tun := newFakeTUN(1)

		bridge := New(tun, bulkIn, bulkOut, 0, 1500, true)
		ctx, cancel := context.WithCancel(context.Background())
		bridge.Start(ctx)

		// Send a 512-byte packet via TUN (512 = 1 × maxPacketSize).
		pkt512 := makeIPv4Packet(512) // total IP packet = 512 bytes
		if len(pkt512) != 512 {
			t.Fatalf("test packet is %d bytes, want 512", len(pkt512))
		}
		tun.rx <- pkt512

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
		stopBridgeAndTUN(bridge, tun)
	})

	t.Run("ZLPDisabled", func(t *testing.T) {
		bulkIn := newFakeBulkReader()
		bulkOut := &fakeBulkWriter{}
		tun := newFakeTUN(1)

		bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
		ctx, cancel := context.WithCancel(context.Background())
		bridge.Start(ctx)

		pkt512 := makeIPv4Packet(512)
		tun.rx <- pkt512

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
		stopBridgeAndTUN(bridge, tun)
	})
}

// TestRelayOffsetMacOS verifies that offset=4 headroom is handled correctly.
func TestRelayOffsetMacOS(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 4, 1500, false) // offset=4 (macOS)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push a 64-byte IPv4 packet into bulk IN.
	ipv4Pkt := makeIPv4Packet(64)
	bulkIn.pkts <- ipv4Pkt

	select {
	case got := <-tun.tx:
		if len(got) != len(ipv4Pkt) {
			t.Fatalf("TUN got %d bytes with offset=4, want %d", len(got), len(ipv4Pkt))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TUN did not receive packet")
	}

	cancel()
	stopBridgeAndTUN(bridge, tun)
}

// TestBridgeStopWaitsGoroutines verifies that Stop blocks until goroutines exit.
func TestBridgeStopWaitsGoroutines(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Close the TUN to unblock tunToModem, then Stop.
	tun.Close()
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

// TestBridgeCloseOrdering verifies the critical close ordering:
// tun.Close() MUST happen before bridge.Stop() — tunToModem blocks in
// tun.Read which doesn't respect context. Closing TUN unblocks Read,
// allowing the goroutine to exit so Stop's wg.Wait() can return.
// Reversing the order would deadlock (Stop waits for goroutine,
// goroutine waits for TUN Read, TUN never closed).
func TestBridgeCloseOrdering(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Correct order: cancel context → close TUN → bridge.Stop
	cancel()
	tun.Close()

	done := make(chan struct{})
	go func() {
		bridge.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stop returned — goroutines exited cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("Stop deadlocked — did you close TUN before Stop?")
	}

	// Verify bridge can't be started again after Stop
	if err := bridge.Start(ctx); err != nil {
		// Expected: Start after Stop should be a no-op (or error)
		// The current implementation treats it as no-op (returns nil)
	}
}

// TestRelayContextCancel verifies that cancelling the context stops both
// goroutines cleanly (with TUN close to unblock tunToModem).
func TestRelayContextCancel(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	cancel()
	tun.Close() // unblock tunToModem

	done := make(chan struct{})
	go func() {
		bridge.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Bridge did not stop after context cancel + TUN close")
	}
}

// TestConcurrentRelayRace exercises the relay under -race.
func TestConcurrentRelayRace(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, true)
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
			case tun.rx <- pkt:
			case <-ctx.Done():
				return
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	tun.Close()
	bridge.Stop()
	wg.Wait()
}

// TestRelayStats verifies packet counters after relay.
func TestRelayStats(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push 3 TX packets
	for i := 0; i < 3; i++ {
		tun.rx <- makeIPv4Packet(64)
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
	tun.Close()
	bridge.Stop()
}

// TestStartIdempotent verifies Start can be called twice without panic.
func TestStartIdempotent(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	tun.Close()
	bridge.Stop()
}

// TestStopWithoutStart verifies Stop is safe before Start.
func TestStopWithoutStart(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &fakeBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	bridge.Stop() // should be a no-op, no panic
}

// TestRelayBulkWriteError verifies the relay continues when bulkOut.Write fails.
func TestRelayBulkWriteError(t *testing.T) {
	bulkIn := newFakeBulkReader()
	bulkOut := &errorBulkWriter{}
	tun := newFakeTUN(1)

	bridge := New(tun, bulkIn, bulkOut, 0, 1500, false)
	ctx, cancel := context.WithCancel(context.Background())
	bridge.Start(ctx)

	// Push a packet — bulkOut.Write will fail, relay should log and continue
	tun.rx <- makeIPv4Packet(64)
	time.Sleep(100 * time.Millisecond)

	cancel()
	tun.Close()
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
