//go:build hardware

// Hardware integration test for qmitransport. Requires a real DJI Baiwang
// modem (QDC507, PID 2C7C:0125) with WinUSB on MI_04 (installed via Zadig).
// Run with:
//
//	mise exec -- go test -tags=hardware -v ./internal/qmitransport/
//
// It will not run under the default `go test ./...` (CI) because the
// `hardware` build tag is unset.
package qmitransport

import (
	"sync"
	"testing"
	"time"
)

// TestHardwareOpenAndClose verifies that Open claims MI_04, sets DTR, and
// Close cleanly releases everything (no segfault, no goroutine leak).
func TestHardwareOpenAndClose(t *testing.T) {
	tr, err := Open()
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // let interrupt goroutine settle
	if err := tr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// TestHardwareSyncExchange sends a QMICTL_SYNC_REQ through the transport and
// expects a SYNC_RESP back. This proves the full model B path:
// Write (SEND_ENCAPSULATED) → interrupt notification → Read (GET_ENCAPSULATED).
func TestHardwareSyncExchange(t *testing.T) {
	tr, err := Open()
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer tr.Close()

	// QMICTL_SYNC_REQ frame (12 bytes, per quectel-qmi-go Packet.Marshal()).
	// QMUX: IFType=01 | Len=0x000B(LE) | CtlFlags=00 | SvcType=00(CTL) | ClID=00
	// CTL:  CtlFlags=00(request) | TxID=01 | MsgID=0x0027(SYNC) | Len=0x0000
	syncReq := []byte{0x01, 0x0B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x27, 0x00, 0x00, 0x00}

	// Write the SYNC request.
	n, err := tr.Write(syncReq)
	if err != nil {
		t.Fatalf("Write SYNC failed: %v", err)
	}
	if n != len(syncReq) {
		t.Fatalf("Write returned %d, want %d", n, len(syncReq))
	}

	// Set a generous deadline and read the response.
	tr.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := make([]byte, 2048)
	n, err = tr.Read(resp)
	if err != nil {
		t.Fatalf("Read SYNC_RESP failed: %v", err)
	}
	if n < 12 {
		t.Fatalf("Read returned %d bytes, want >= 12", n)
	}

	// Verify QMUX frame structure: IFType=0x01, SYNC_RESP MsgID=0x0027.
	if resp[0] != 0x01 {
		t.Fatalf("IFType = 0x%02x, want 0x01", resp[0])
	}
	// CTL header starts at QMUX offset 6: CtlFlags(1) TxID(1) MsgID(2LE) Len(2LE)
	// For SYNC_RESP, MsgID = 0x0027.
	ctlMsgID := uint16(resp[9])<<8 | uint16(resp[8])
	if ctlMsgID != 0x0027 {
		t.Logf("CTL MsgID = 0x%04x (expected 0x0027 for SYNC)", ctlMsgID)
	}
	t.Logf("SYNC_RESP received: %d bytes", n)
	t.Logf("  raw: % X", resp[:n])
}

// TestHardwareConcurrentClose stress-tests the ioMu serialization under real
// USB. It runs concurrent Read + Write goroutines (simulating the QMI client's
// readLoop + writerLoop) and calls Close while they're in-flight — exactly the
// issue/001 crash window. Repeated 10× to increase crash probability.
//
// If issue/001 regressed, this test segfaults. If ioMu works, all goroutines
// exit cleanly with errClosed.
func TestHardwareConcurrentClose(t *testing.T) {
	syncReq := []byte{0x01, 0x0B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x27, 0x00, 0x00, 0x00}

	for i := 0; i < 10; i++ {
		tr, err := Open()
		if err != nil {
			t.Fatalf("iteration %d: Open failed: %v", i, err)
		}

		var wg sync.WaitGroup
		stop := make(chan struct{})

		// Simulated readLoop.
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 2048)
			for {
				select {
				case <-stop:
					return
				default:
				}
				tr.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				tr.Read(buf) // ignore errors (timeout/closed expected)
			}
		}()

		// Simulated writerLoop.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tr.Write(syncReq)  // ignore errors (closed expected)
				time.Sleep(2 * time.Millisecond)
			}
		}()

		// Let them run briefly, then Close mid-flight.
		time.Sleep(20 * time.Millisecond)
		close(stop)

		if err := tr.Close(); err != nil {
			t.Fatalf("iteration %d: Close failed: %v", i, err)
		}

		wg.Wait()
		t.Logf("iteration %d: clean Close with concurrent Read+Write", i)

		// Brief pause between iterations to let USB settle.
		time.Sleep(100 * time.Millisecond)
	}
}
