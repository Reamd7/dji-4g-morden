package testutil

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestScriptPortWriteRead(t *testing.T) {
	p := NewScriptPort([]byte("hello"))

	if n, err := p.Write([]byte("AT\r\n")); err != nil || n != 4 {
		t.Fatalf("Write = (%d, %v), want (4, nil)", n, err)
	}

	buf := make([]byte, 16)
	n, err := p.Read(buf)
	if err != nil || n != 5 {
		t.Fatalf("Read = (%d, %v), want (5, nil)", n, err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("Read data = %q, want %q", buf[:n], "hello")
	}
}

func TestScriptPortWrittenRecordsAll(t *testing.T) {
	p := NewScriptPort(nil)
	p.Write([]byte("foo"))
	p.Write([]byte("bar"))

	want := []byte("foobar")
	if got := p.Written(); !bytes.Equal(got, want) {
		t.Errorf("Written() = %q, want %q", got, want)
	}

	// Written must return a copy — mutating it must not affect the port.
	got := p.Written()
	got[0] = 'X'
	again := p.Written()
	if again[0] != 'f' {
		t.Errorf("Written() returned alias, mutation leaked: %q", again)
	}
}

func TestScriptPortCloseTerminatesRead(t *testing.T) {
	p := NewScriptPort(nil) // empty readData: Read would block

	// Start a read that will block, then close from another goroutine.
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 8)
		_, err := p.Read(buf)
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	p.Close()

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Errorf("Read after Close = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not return after Close (deadlock)")
	}

	// Subsequent Write on closed port must error.
	if _, err := p.Write([]byte("x")); err == nil {
		t.Error("Write on closed port succeeded, want error")
	}
	if !p.IsClosed() {
		t.Error("IsClosed() = false after Close")
	}
}

func TestScriptPortReadDeadline(t *testing.T) {
	p := NewScriptPort(nil)
	// Deadline in the past → Read should return io.EOF immediately.
	p.SetReadDeadline(time.Now().Add(-time.Millisecond))

	buf := make([]byte, 8)
	_, err := p.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("Read past deadline = %v, want io.EOF", err)
	}
}

// Compile-time guarantee: ScriptPort satisfies the interfaces our transport
// layer (and upstream quectel-qmi-go / uicc-go) depend on.
var _ io.ReadWriteCloser = (*ScriptPort)(nil)
