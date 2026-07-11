// Package testutil provides in-memory fakes for transport testing, mirroring
// the hand-written mock style used in uicc-go/at/at_test.go (scriptPort).
//
// These let transport/protocol tests run entirely offline — no real USB device,
// no libusb, no cgo. See AGENTS.md "测试方案" for the layering rationale.
package testutil

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ScriptPort is an in-memory io.ReadWriteCloser that replays a preloaded
// response and records everything written to it.
//
// Read semantics: drains preloaded bytes; when exhausted it BLOCKS until more
// data is written, Close is called, or the read deadline passes. This mirrors
// a real blocking serial/USB read and lets tests drive read loops deterministically.
//
// Usage: preload ReadData with the bytes the fake "device" should return, run
// the code under test, then assert on Written.
type ScriptPort struct {
	mu       sync.Mutex
	readData []byte
	written  []byte
	closed   bool
	cond     *sync.Cond
	// readDeadline, if set, makes Read return io.EOF once the deadline passes
	// (even if readData is empty). Lets callers exercise read-timeout paths.
	readDeadline time.Time
}

// NewScriptPort returns a ScriptPort preloaded with the given response bytes.
func NewScriptPort(response []byte) *ScriptPort {
	p := &ScriptPort{readData: append([]byte(nil), response...)}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Read drains preloaded bytes. Once exhausted it blocks until Write adds more,
// Close is called, or readDeadline passes.
func (p *ScriptPort) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for {
		if p.closed {
			return 0, io.EOF
		}
		if len(p.readData) > 0 {
			n := copy(buf, p.readData)
			p.readData = p.readData[n:]
			return n, nil
		}
		if !p.readDeadline.IsZero() && time.Now().After(p.readDeadline) {
			return 0, io.EOF
		}
		// Block until Write/Close/SetReadDeadline signals. Wait releases the
		// mutex while parked, so writers can make progress.
		p.cond.Wait()
		// Loop back: re-check closed / readData / deadline after waking.
	}
}

// Write appends to the recorded Written buffer and wakes any blocked reader.
func (p *ScriptPort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, errors.New("write on closed port")
	}
	p.written = append(p.written, data...)
	p.cond.Broadcast()
	return len(data), nil
}

// Close marks the port closed and wakes any blocked reader; subsequent Read
// returns io.EOF.
func (p *ScriptPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
	return nil
}

// Feed appends bytes to the readable buffer as if the device sent them
// asynchronously (e.g. an AT response or a URC). Unlike Write, which records
// host-to-device bytes, Feed makes bytes available to subsequent Read calls.
// Wakes any blocked reader so the new data is observed promptly.
func (p *ScriptPort) Feed(data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readData = append(p.readData, data...)
	p.cond.Broadcast()
}

// Written returns a copy of all bytes written to the port so far.
func (p *ScriptPort) Written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]byte, len(p.written))
	copy(out, p.written)
	return out
}

// IsClosed reports whether Close has been called.
func (p *ScriptPort) IsClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// SetReadDeadline sets a deadline after which Read returns io.EOF. Used to
// exercise read-timeout paths without real I/O latency. Wakes blocked readers
// so they observe the new deadline promptly.
func (p *ScriptPort) SetReadDeadline(t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readDeadline = t
	p.cond.Broadcast()
}

// Compile-time interface checks.
var (
	_ io.ReadWriteCloser = (*ScriptPort)(nil)
)
