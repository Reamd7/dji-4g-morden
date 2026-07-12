// Package usbtransport wraps DJI Baiwang modem USB bulk endpoints as an
// io.ReadWriteCloser, ready to feed into modem.NewFromIO.
//
// Read blocks until data arrives or the transport is Closed — it does NOT use
// a short-timeout poll. A 200ms poll would cancel the USB transfer every cycle
// (~5 libusb_cancel_transfer/s), which segfaults WinUSB under send-time
// read/write concurrency. Instead Read uses a long-lived context cancelled only
// by Close, so the transfer stays submitted (zero cancels) during normal
// operation. See issue/001-gousb-close-transfer-cancel-crash.md.
package usbtransport

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/google/gousb"
)

// sharedContext returns a process-wide singleton gousb.Context.
//
// gousb.Context wraps a libusb context that should be reused across device
// opens. Creating a fresh context per Open and closing it on Close is unsafe
// on macOS: libusb_init_context fails (LIBUSB_ERROR_OTHER, code -99) when a
// second context is initialized after the first was torn down in the same
// process, and gousb panics on that error. A single shared context avoids
// repeated init/exit and matches gousb's intended usage (one context, many
// devices). The context is never closed; it lives for the process lifetime
// (OS reclaims it on exit), correct for both CLI tools and tests.
var (
	sharedCtxOnce sync.Once
	sharedCtx     *gousb.Context
)

func sharedContext() *gousb.Context {
	sharedCtxOnce.Do(func() {
		sharedCtx = gousb.NewContext()
	})
	return sharedCtx
}

// ATTransport wraps MI_02 (AT command port) USB bulk endpoints as an
// io.ReadWriteCloser. It is the USB-side counterpart of go.bug.st/serial's
// serial.Port for the AT command channel.
type ATTransport struct {
	ctx   *gousb.Context
	dev   *gousb.Device
	cfg   *gousb.Config
	iface *gousb.Interface
	out   endpointWriter // EP 0x03 OUT bulk (gousb *OutEndpoint)
	in    endpointReader // EP 0x84 IN bulk  (gousb *InEndpoint)

	// readCtx is a long-lived context for the IN endpoint read. It has no
	// deadline — Read blocks until data arrives or readCancel fires on Close.
	// This avoids the per-read libusb_cancel_transfer that a short-timeout
	// poll would trigger (~5/s at 200ms), which segfaults WinUSB under
	// send-time read/write concurrency. See issue/001-gousb-close-transfer-cancel-crash.md.
	readCtx    context.Context
	readCancel context.CancelFunc

	mu     sync.Mutex
	closed bool
}

// endpointReader abstracts the gousb IN endpoint so the Read short-timeout
// logic can be unit-tested with testutil.ScriptPort (no gousb / hardware).
// gousb's *InEndpoint satisfies this signature directly.
type endpointReader interface {
	ReadContext(ctx context.Context, buf []byte) (int, error)
}

// endpointWriter abstracts the gousb OUT endpoint. gousb's *OutEndpoint
// satisfies this signature directly.
type endpointWriter interface {
	Write(buf []byte) (int, error)
}

// Open opens the specified interface on device vid:pid and returns an
// ATTransport wrapping its bulk IN/OUT endpoints.
//
// For the DJI Baiwang AT command port (EC25 PID 0x0125): ifaceNum=2,
// epOut=0x03, epIn=0x84 (see AGENTS.md "实测验证结果").
//
// The returned ATTransport must be Close()d to release the USB interface
// claim and configuration.
func Open(vid, pid uint16, ifaceNum, epOut, epIn int) (*ATTransport, error) {
	ctx := sharedContext()
	dev, err := ctx.OpenDeviceWithVIDPID(gousb.ID(vid), gousb.ID(pid))
	if err != nil {
		return nil, fmt.Errorf("usbtransport: open %04x:%04x: %w", vid, pid, err)
	}
	if dev == nil {
		return nil, fmt.Errorf("usbtransport: device %04x:%04x not found", vid, pid)
	}

	cfg, err := dev.Config(1)
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("usbtransport: config 1: %w", err)
	}
	iface, err := cfg.Interface(ifaceNum, 0)
	if err != nil {
		cfg.Close()
		dev.Close()
		return nil, fmt.Errorf("usbtransport: claim interface %d: %w", ifaceNum, err)
	}
	out, err := iface.OutEndpoint(epOut)
	if err != nil {
		iface.Close()
		cfg.Close()
		dev.Close()
		return nil, fmt.Errorf("usbtransport: OUT endpoint 0x%02x: %w", epOut, err)
	}
	in, err := iface.InEndpoint(epIn)
	if err != nil {
		iface.Close()
		cfg.Close()
		dev.Close()
		return nil, fmt.Errorf("usbtransport: IN endpoint 0x%02x: %w", epIn, err)
	}

	readCtx, readCancel := context.WithCancel(context.Background())
	return &ATTransport{
		ctx:        ctx,
		dev:        dev,
		cfg:        cfg,
		iface:      iface,
		out:        out,
		in:         in,
		readCtx:    readCtx,
		readCancel: readCancel,
	}, nil
}

// Read implements io.Reader. It blocks until data arrives or the transport is
// Closed (readCancel fires). Unlike a short-timeout poll, this never cancels
// the underlying USB transfer during normal operation — it only cancels once,
// at Close. This avoids the libusb_cancel_transfer segfault that a 200ms poll
// would trigger under send-time read/write concurrency (issue/001).
//
// On Close, readCtx is cancelled; gousb returns a context.Canceled error,
// which implements Timeout()==true, so modem.readerLoop's isTimeout() treats
// it as nothing-to-do and the loop exits via the m.closed check on the next
// iteration.
func (t *ATTransport) Read(buf []byte) (int, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return 0, fmt.Errorf("usbtransport: read on closed transport")
	}
	t.mu.Unlock()

	return t.in.ReadContext(t.readCtx, buf)
}

// Write implements io.Writer. It writes the full buffer to the OUT endpoint;
// gousb's OutEndpoint.Write blocks until the transfer completes.
func (t *ATTransport) Write(buf []byte) (int, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return 0, fmt.Errorf("usbtransport: write on closed transport")
	}
	t.mu.Unlock()
	return t.out.Write(buf)
}

// Close releases the USB interface claim, configuration, and device.
//
// readCancel fires FIRST, before any USB handle is released, so that an
// in-flight Read returns (its transfer is cancelled cleanly) before the
// underlying interface/config/device/context are torn down. Releasing USB
// handles while a transfer is still submitted would segfault libusb
// (issue/001).
func (t *ATTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	// Cancel the in-flight read so readerLoop's blocked ReadContext returns
	// before we release the USB handles below.
	if t.readCancel != nil {
		t.readCancel()
	}
	var errs []error
	if t.iface != nil {
		t.iface.Close()
	}
	if t.cfg != nil {
		if err := t.cfg.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if t.dev != nil {
		if err := t.dev.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 1 {
		return errs[0]
	}
	if len(errs) > 1 {
		return fmt.Errorf("usbtransport: close errors: %v", errs)
	}
	return nil
}

// Compile-time interface checks.
var (
	_ endpointReader = (*gousb.InEndpoint)(nil)
	_ endpointWriter = (*gousb.OutEndpoint)(nil)
	// ATTransport satisfies the transport contract modem.NewFromIO expects.
	_ io.ReadWriteCloser = (*ATTransport)(nil)
)
