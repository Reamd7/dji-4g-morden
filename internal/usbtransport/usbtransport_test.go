package usbtransport

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"dji-modem-research/internal/testutil"
	modem "dji-modem-research/third_party/sms-gateway/modem"
)

// scriptReader adapts testutil.ScriptPort (io.ReadWriteCloser) to the
// endpointReader interface (ReadContext). It runs the blocking ScriptPort.Read
// in a goroutine and races it against the context deadline, mimicking how a
// real gousb InEndpoint.ReadContext times out.
type scriptReader struct{ port *testutil.ScriptPort }

func (r *scriptReader) ReadContext(ctx context.Context, buf []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := r.port.Read(buf)
		ch <- result{n, err}
	}()
	select {
	case res := <-ch:
		return res.n, res.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// scriptWriter adapts testutil.ScriptPort to the endpointWriter interface.
type scriptWriter struct{ port *testutil.ScriptPort }

func (w *scriptWriter) Write(buf []byte) (int, error) {
	return w.port.Write(buf)
}

// newTestTransport builds an ATTransport wired to a ScriptPort pair, bypassing
// gousb entirely. The cleanup func closes the ScriptPort (and thus the
// transport's blocked reads).
func newTestTransport(response []byte) (*ATTransport, *testutil.ScriptPort) {
	port := testutil.NewScriptPort(response)
	t := &ATTransport{
		in:  &scriptReader{port: port},
		out: &scriptWriter{port: port},
	}
	return t, port
}

// --- Compile-time interface checks ---

var (
	_ endpointReader = (*scriptReader)(nil)
	_ endpointWriter = (*scriptWriter)(nil)
	_ io.ReadWriter  = (*ATTransport)(nil)
)

// --- Read tests ---

func TestReadReturnsData(t *testing.T) {
	tt, _ := newTestTransport([]byte("AT\r\nOK\r\n"))
	buf := make([]byte, 64)
	n, err := tt.Read(buf)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if got := string(buf[:n]); got != "AT\r\nOK\r\n" {
		t.Errorf("Read data = %q, want %q", got, "AT\r\nOK\r\n")
	}
}

func TestReadPartialData(t *testing.T) {
	tt, _ := newTestTransport([]byte("hello world"))
	buf := make([]byte, 5)
	n, err := tt.Read(buf)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if n != 5 || string(buf) != "hello" {
		t.Errorf("Read = %q (%d bytes), want \"hello\" (5)", buf[:n], n)
	}
}

// TestReadTimeoutReturnsTimeoutMethod is the critical contract for
// modem.readerLoop: when no data arrives within readPollInterval, Read must
// return an error whose Timeout() method yields true so isTimeout() recognizes
// it as nothing-to-do and the loop continues. context.DeadlineExceeded
// (returned by the scriptReader on ctx.Done) satisfies this.
func TestReadTimeoutReturnsTimeoutMethod(t *testing.T) {
	tt, _ := newTestTransport(nil) // empty → ScriptPort.Read blocks
	buf := make([]byte, 64)

	done := make(chan struct{})
	go func() {
		_, err := tt.Read(buf)
		if err == nil {
			t.Error("Read returned nil error, want timeout")
		}
		var timeout interface{ Timeout() bool }
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Errorf("Read error = %v, want an error implementing Timeout()==true", err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not return within 2s (expected ~readPollInterval timeout)")
	}
}

// --- Write tests ---

func TestWriteFull(t *testing.T) {
	tt, port := newTestTransport(nil)
	cmd := []byte("AT+CSQ\r\n")
	n, err := tt.Write(cmd)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(cmd) {
		t.Errorf("Write returned n=%d, want %d", n, len(cmd))
	}
	if got := port.Written(); string(got) != string(cmd) {
		t.Errorf("Written = %q, want %q", got, cmd)
	}
}

// --- Close tests ---

func TestCloseTerminatesRead(t *testing.T) {
	tt, _ := newTestTransport(nil)

	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 8)
		_, err := tt.Read(buf)
		errCh <- err
	}()

	// Give the reader a moment to park on ScriptPort.Read.
	time.Sleep(20 * time.Millisecond)
	if err := tt.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	select {
	case <-errCh:
		// After Close the reader goroutine is still racing ctx vs ScriptPort.
		// ScriptPort.Close wakes the blocked Read with io.EOF. Either an EOF
		// (from ScriptPort) or a context error (from the poll deadline) is
		// acceptable; what matters is that Read returns rather than hanging.
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not return after Close (deadlock)")
	}
}

func TestCloseIdempotent(t *testing.T) {
	tt, _ := newTestTransport(nil)
	if err := tt.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tt.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestReadAfterClose(t *testing.T) {
	tt, _ := newTestTransport(nil)
	if err := tt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	buf := make([]byte, 8)
	if _, err := tt.Read(buf); err == nil {
		t.Error("Read after Close returned nil error, want error")
	}
}

func TestWriteAfterClose(t *testing.T) {
	tt, _ := newTestTransport(nil)
	if err := tt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := tt.Write([]byte("x")); err == nil {
		t.Error("Write after Close returned nil error, want error")
	}
}

// --- Concurrency test (must pass under -race) ---

func TestConcurrentReadWrite(t *testing.T) {
	tt, port := newTestTransport(nil)
	defer tt.Close()

	const writers = 4
	const msgsPerWriter = 25
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < msgsPerWriter; j++ {
				_, _ = tt.Write([]byte("ping\n"))
			}
		}()
	}

	// One reader draining in parallel; port has no preloaded data so reads
	// time out at readPollInterval, which is the realistic idle-channel case.
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 32)
		for {
			select {
			case <-done:
				return
			default:
				_, _ = tt.Read(buf)
			}
		}
	}()

	wg.Wait()
	close(done)

	// All writer bytes must be recorded by the port, in order per writer
	// (ScriptPort serializes Writes under its mutex).
	total := writers * msgsPerWriter * len("ping\n")
	if got := len(port.Written()); got != total {
		t.Errorf("total written = %d, want %d", got, total)
	}
}

// --- Integration with modem.NewFromIO (the real purpose of this transport) ---

func TestATTransportFeedsModemNewFromIO(t *testing.T) {
	// Preload a full AT session: echo + OK for "AT", then +CSQ line + OK.
	// This proves the ATTransport + ScriptPort plumbing drives the modem
	// readerLoop end-to-end without any USB hardware.
	port := testutil.NewScriptPort(nil)
	tt := &ATTransport{
		in:  &scriptReader{port: port},
		out: &scriptWriter{port: port},
	}
	defer tt.Close()

	// Feed the device's response asynchronously. ScriptPort.Feed injects
	// device-sent bytes (unlike Write, which records host-to-device bytes).
	// Wait for readerLoop to start and the AT+CSQ Write to land, then feed
	// the full response: echo + OK would be for "AT+CSQ" but Initialize isn't
	// called here; the modem strips the echo line matching the cmd anyway.
	resp := "AT+CSQ\r\n+CSQ: 23,0\r\nOK\r\n"
	go func() {
		time.Sleep(80 * time.Millisecond) // let readerLoop latch the pending call
		port.Feed([]byte(resp))
	}()

	m := modem.NewFromIO(tt)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lines, err := m.SendAndWait(ctx, "AT+CSQ", 2*time.Second)
	if err != nil {
		t.Fatalf("SendAndWait: %v", err)
	}
	want := "+CSQ: 23,0"
	found := false
	for _, l := range lines {
		if l == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SendAndWait lines = %v, want to contain %q", lines, want)
	}
	// Verify the modem actually wrote AT+CSQ\r\n through our transport.
	if w := port.Written(); !strings.HasSuffix(string(w), "AT+CSQ\r\n") {
		t.Errorf("modem wrote %q, want suffix %q", w, "AT+CSQ\r\n")
	}
}
