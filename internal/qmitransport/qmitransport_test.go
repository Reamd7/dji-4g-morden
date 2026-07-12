package qmitransport

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockControlDevice implements controlDevice for offline testing.
// It records all Control calls and can simulate latency.
type mockControlDevice struct {
	mu       sync.Mutex
	calls    []mockControlCall
	getData  []byte // data to return for GET_ENCAPSULATED_RESPONSE
	sendOK   bool
	closed   atomic.Bool
	getDelay time.Duration
}

type mockControlCall struct {
	rType   uint8
	request uint8
	val     uint16
	idx     uint16
	dataLen int
}

func (m *mockControlDevice) Control(rType, request uint8, val, idx uint16, data []byte) (int, error) {
	m.mu.Lock()
	m.calls = append(m.calls, mockControlCall{rType, request, val, idx, len(data)})
	m.mu.Unlock()

	if m.closed.Load() {
		return 0, errors.New("device closed")
	}

	switch {
	case rType == 0xA1 && request == 0x01: // GET_ENCAPSULATED_RESPONSE
		if m.getDelay > 0 {
			time.Sleep(m.getDelay)
		}
		n := copy(data, m.getData)
		return n, nil
	case rType == 0x21 && request == 0x00: // SEND_ENCAPSULATED_COMMAND
		return len(data), nil
	case rType == 0x21 && request == 0x22: // SET_CONTROL_LINE_STATE (DTR)
		return 0, nil
	default:
		return 0, errors.New("unexpected control request")
	}
}

// mockInterruptReader implements interruptReader for offline testing.
// It blocks on a context until signalled, then returns data.
type mockInterruptReader struct {
	notifyCh chan struct{}
}

func (m *mockInterruptReader) ReadContext(ctx context.Context, buf []byte) (int, error) {
	select {
	case <-m.notifyCh:
		// Simulate RESPONSE_AVAILABLE (8 bytes)
		data := []byte{0xa1, 0x01, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00}
		n := copy(buf, data)
		return n, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// newTestTransport creates a QMITransport with mock USB dependencies.
// The interrupt goroutine is started. Call Close to stop it.
func newTestTransport(ctrl *mockControlDevice, intr *mockInterruptReader) *QMITransport {
	readCtx, readCancel := context.WithCancel(context.Background())
	t := &QMITransport{
		ctrl:       ctrl,
		intr:       intr,
		ifaceNum:   4,
		readCtx:    readCtx,
		readCancel: readCancel,
		notifyCh:   make(chan struct{}, 1),
		intrDone:   make(chan struct{}),
	}
	go t.interruptLoop()
	return t
}

// TestConcurrentReadWriteClose exercises the ioMu serialization under -race.
// Multiple goroutines call Read, Write, and Close concurrently. The test
// verifies no data race is detected and Close returns without panic.
func TestConcurrentReadWriteClose(t *testing.T) {
	for i := 0; i < 50; i++ {
		ctrl := &mockControlDevice{
			getData: []byte{0x01, 0x0B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x27, 0x00, 0x00, 0x00},
			sendOK:  true,
		}
		intr := &mockInterruptReader{notifyCh: make(chan struct{})}
		tr := newTestTransport(ctrl, intr)

		var wg sync.WaitGroup
		var readErrs, writeErrs atomic.Int32

		// Simulated readLoop: Read in a loop until error.
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 2048)
			for {
				tr.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
				_, err := tr.Read(buf)
				if err != nil {
					readErrs.Add(1)
					return
				}
			}
		}()

		// Simulated writerLoop: Write in a loop until error.
		wg.Add(1)
		go func() {
			defer wg.Done()
			frame := []byte{0x01, 0x0B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x27, 0x00, 0x00, 0x00}
			for {
				_, err := tr.Write(frame)
				if err != nil {
					writeErrs.Add(1)
					return
				}
				time.Sleep(time.Millisecond) // small delay between writes
			}
		}()

		// Let them run briefly, then Close (simulates client.Close ordering).
		time.Sleep(5 * time.Millisecond)

		// Signal interrupt reader to provide data (triggers Read's GET path).
		select {
		case intr.notifyCh <- struct{}{}:
		default:
		}

		// Close while Read/Write may be in-flight.
		if err := tr.Close(); err != nil {
			t.Fatalf("iteration %d: Close failed: %v", i, err)
		}

		wg.Wait()

		// After Close, Read and Write should return errClosed.
		_, rErr := tr.Read(make([]byte, 10))
		if rErr == nil {
			t.Fatalf("iteration %d: Read after Close should fail", i)
		}
		_, wErr := tr.Write([]byte{0x01})
		if wErr == nil {
			t.Fatalf("iteration %d: Write after Close should fail", i)
		}

		// Double Close should be a no-op.
		if err := tr.Close(); err != nil {
			t.Fatalf("iteration %d: double Close failed: %v", i, err)
		}
	}
}

// TestReadTimeout verifies that Read returns a timeout error when the
// deadline expires without any interrupt notification.
func TestReadTimeout(t *testing.T) {
	ctrl := &mockControlDevice{getData: []byte{0x01}}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)
	defer tr.Close()

	tr.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	_, err := tr.Read(make([]byte, 2048))
	if err == nil {
		t.Fatal("Read should timeout")
	}
	var timeout errTimeout
	if !errors.As(err, &timeout) {
		t.Fatalf("Read returned %v, want errTimeout", err)
	}
}

// TestReadAfterClose verifies that Read on a closed transport returns errClosed.
func TestReadAfterClose(t *testing.T) {
	ctrl := &mockControlDevice{}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)

	tr.Close()
	_, err := tr.Read(make([]byte, 2048))
	if !errors.Is(err, errClosed) {
		t.Fatalf("Read after Close returned %v, want errClosed", err)
	}
}

// TestWriteAfterClose verifies that Write on a closed transport returns errClosed.
func TestWriteAfterClose(t *testing.T) {
	ctrl := &mockControlDevice{}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)

	tr.Close()
	_, err := tr.Write([]byte{0x01})
	if !errors.Is(err, errClosed) {
		t.Fatalf("Write after Close returned %v, want errClosed", err)
	}
}

// TestReadGetsResponse verifies the happy path: interrupt notification →
// control GET returns the QMUX frame.
func TestReadGetsResponse(t *testing.T) {
	resp := []byte{0x01, 0x0B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x27, 0x00, 0x00, 0x00}
	ctrl := &mockControlDevice{getData: resp}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)
	defer tr.Close()

	// Signal that data is available.
	intr.notifyCh <- struct{}{}

	tr.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, err := tr.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != len(resp) {
		t.Fatalf("Read returned %d bytes, want %d", n, len(resp))
	}
	// Verify QMUX IFType.
	if buf[0] != 0x01 {
		t.Fatalf("IFType = 0x%02x, want 0x01", buf[0])
	}
}
