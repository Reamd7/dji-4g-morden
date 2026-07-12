package qmitransport

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dji-modem-research/third_party/quectel-qmi-go/qmi"
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
func newTestTransport(ctrl controlDevice, intr *mockInterruptReader) *QMITransport {
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

// TestReadBlocksUntilClose verifies that Read blocks indefinitely (until the
// deadline) when no data is available, and that Close unblocks it via
// readCancel → readCtx.Done().
func TestReadBlocksUntilClose(t *testing.T) {
	ctrl := &mockControlDevice{getData: []byte{0x01}}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)

	readDone := make(chan error, 1)
	go func() {
		tr.SetReadDeadline(time.Now().Add(10 * time.Second)) // long deadline
		_, err := tr.Read(make([]byte, 2048))
		readDone <- err
	}()

	// Ensure Read is blocked in the select.
	time.Sleep(20 * time.Millisecond)

	tr.Close()

	select {
	case err := <-readDone:
		if !errors.Is(err, errClosed) {
			t.Fatalf("Read returned %v, want errClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not return within 2s after Close")
	}
}

// TestWriteFull verifies that Write returns the full byte count and that the
// mock records the SEND_ENCAPSULATED_COMMAND call with correct parameters.
func TestWriteFull(t *testing.T) {
	frame := []byte{0x01, 0x0B, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x27, 0x00, 0x00, 0x00}
	ctrl := &mockControlDevice{sendOK: true}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)
	defer tr.Close()

	n, err := tr.Write(frame)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(frame) {
		t.Fatalf("Write returned %d, want %d", n, len(frame))
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if len(ctrl.calls) != 1 {
		t.Fatalf("expected 1 Control call, got %d", len(ctrl.calls))
	}
	c := ctrl.calls[0]
	if c.rType != bmReqClassOut || c.request != reqSendEncap {
		t.Fatalf("expected SEND_ENCAPSULATED (0x%02x,0x%02x), got (0x%02x,0x%02x)",
			bmReqClassOut, reqSendEncap, c.rType, c.request)
	}
	if c.dataLen != len(frame) {
		t.Fatalf("recorded data length %d, want %d", c.dataLen, len(frame))
	}
}

// reactiveControlDevice is a mock controlDevice that triggers an interrupt
// notification when SEND_ENCAPSULATED_COMMAND is received, simulating the
// modem's RESPONSE_AVAILABLE notification. This lets the QMI client's
// readLoop receive the response to its request.
type reactiveControlDevice struct {
	mu      sync.Mutex
	calls   []mockControlCall
	getData []byte
	intrCh  chan struct{} // shared with the interrupt reader
}

func (m *reactiveControlDevice) Control(rType, request uint8, val, idx uint16, data []byte) (int, error) {
	m.mu.Lock()
	m.calls = append(m.calls, mockControlCall{rType, request, val, idx, len(data)})
	m.mu.Unlock()

	switch {
	case rType == bmReqClassIn && request == reqGetEncap: // GET_ENCAPSULATED_RESPONSE
		m.mu.Lock()
		d := m.getData
		m.mu.Unlock()
		n := copy(data, d)
		return n, nil
	case rType == bmReqClassOut && request == reqSendEncap: // SEND_ENCAPSULATED_COMMAND
		// Simulate the modem raising RESPONSE_AVAILABLE on the interrupt EP.
		select {
		case m.intrCh <- struct{}{}:
		default:
		}
		return len(data), nil
	case rType == bmReqClassOut && request == reqSetCtrlLine: // DTR
		return 0, nil
	default:
		return 0, errors.New("unexpected control request")
	}
}

// TestQMUXFrameRoundTrip injects a mock transport into qmi.NewClientFromTransport
// and verifies the SYNC exchange works end-to-end: the client sends SYNC_REQ
// (Write → SEND_ENCAPSULATED), the mock responds with SYNC_RESP (interrupt
// notification → GET_ENCAPSULATED_RESPONSE), and the client's readLoop
// delivers the response to Sync().
func TestQMUXFrameRoundTrip(t *testing.T) {
	// SYNC_RESP from Phase 0 probe (19 bytes, result=SUCCESS).
	// QMUX: IFType=01 | Len=0x0012 | CtlFlg=80 | SvcType=00(CTL) | ClID=00
	// CTL:  CtlFlg=01(response) | TxID=01 | MsgID=0x0027 | Len=0x0007
	// TLV:  Type=02 | Len=04 | Value=0x00000000(SUCCESS)
	syncResp := []byte{
		0x01, 0x12, 0x00, 0x80, 0x00, 0x00,
		0x01, 0x01, 0x27, 0x00, 0x07, 0x00,
		0x02, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00,
	}

	intrCh := make(chan struct{}, 1)
	ctrl := &reactiveControlDevice{
		getData: syncResp,
		intrCh:  intrCh,
	}
	intr := &mockInterruptReader{notifyCh: intrCh}

	readCtx, readCancel := context.WithCancel(context.Background())
	transport := &QMITransport{
		ctrl:       ctrl,
		intr:       intr,
		ifaceNum:   4,
		readCtx:    readCtx,
		readCancel: readCancel,
		notifyCh:   make(chan struct{}, 1),
		intrDone:   make(chan struct{}),
	}
	go transport.interruptLoop()
	defer transport.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := qmi.DefaultClientOptions()
	client, err := qmi.NewClientFromTransport(ctx, transport, opts)
	if err != nil {
		t.Fatalf("NewClientFromTransport failed: %v", err)
	}
	defer client.Close()

	// Verify SYNC_REQ was sent via SEND_ENCAPSULATED_COMMAND.
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	foundSend := false
	for _, c := range ctrl.calls {
		if c.rType == bmReqClassOut && c.request == reqSendEncap {
			foundSend = true
			if c.dataLen < 12 {
				t.Fatalf("SEND data too short: %d bytes", c.dataLen)
			}
			break
		}
	}
	if !foundSend {
		t.Fatal("no SEND_ENCAPSULATED_COMMAND call recorded (SYNC not sent)")
	}
}

// TestReadGETError verifies that Read wraps control transfer errors.
func TestReadGETError(t *testing.T) {
	ctrl := &errorControlDevice{getErr: errors.New("USB GET failed")}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)
	defer tr.Close()

	intr.notifyCh <- struct{}{}
	tr.SetReadDeadline(time.Now().Add(time.Second))
	_, err := tr.Read(make([]byte, 2048))
	if err == nil {
		t.Fatal("Read should fail when GET returns error")
	}
}

// TestWriteSENDError verifies that Write wraps control transfer errors.
func TestWriteSENDError(t *testing.T) {
	ctrl := &errorControlDevice{sendErr: errors.New("USB SEND failed")}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)
	defer tr.Close()

	_, err := tr.Write([]byte{0x01, 0x0B})
	if err == nil {
		t.Fatal("Write should fail when SEND returns error")
	}
}

// TestErrTimeoutInterface verifies errTimeout satisfies the timeout interface.
func TestErrTimeoutInterface(t *testing.T) {
	e := errTimeout{}
	if e.Error() == "" {
		t.Fatal("Error() should return non-empty string")
	}
	if !e.Timeout() {
		t.Fatal("Timeout() should return true")
	}
	if !e.Temporary() {
		t.Fatal("Temporary() should return true")
	}
	// Verify os.IsTimeout recognizes it.
	if !os.IsTimeout(e) {
		t.Fatal("os.IsTimeout should return true for errTimeout")
	}
}

// errorControlDevice is a mock that returns configurable errors.
type errorControlDevice struct {
	getErr  error
	sendErr error
}

func (m *errorControlDevice) Control(rType, request uint8, val, idx uint16, data []byte) (int, error) {
	switch {
	case rType == bmReqClassIn && request == reqGetEncap:
		return 0, m.getErr
	case rType == bmReqClassOut && request == reqSendEncap:
		return 0, m.sendErr
	case rType == bmReqClassOut && request == reqSetCtrlLine:
		return 0, nil
	default:
		return 0, errors.New("unexpected control request")
	}
}
