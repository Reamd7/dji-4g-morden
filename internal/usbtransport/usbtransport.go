// Package usbtransport wraps DJI Baiwang modem USB bulk endpoints as an
// io.ReadWriteCloser, ready to feed into modem.NewFromIO.
//
// Read uses a short-timeout poll: each Read blocks at most readPollInterval
// (200ms) and, on timeout, returns an error implementing Timeout() so the
// modem readerLoop can wake up, observe Close, and treat the timeout as
// nothing-to-do. This mirrors go.bug.st/serial's SetReadTimeout(200ms)
// semantic that the upstream modem package was designed against.
package usbtransport

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/gousb"
)

// readPollInterval bounds how long a single Read call blocks when no data
// arrives. It mirrors the 200ms serial read timeout the modem.readerLoop was
// designed against (see Open's SetReadTimeout in third_party/sms-gateway/modem).
// Shorter = more responsive shutdown, more CPU; 200ms is the proven value.
const readPollInterval = 200 * time.Millisecond

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
	ctx := gousb.NewContext()
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

	return &ATTransport{
		ctx:   ctx,
		dev:   dev,
		cfg:   cfg,
		iface: iface,
		out:   out,
		in:    in,
	}, nil
}

// Read implements io.Reader. It blocks at most readPollInterval (200ms); on
// timeout it returns an error whose Timeout() method yields true so that
// modem.readerLoop treats it as nothing-to-do and loops (and, critically, can
// observe m.closed between polls).
//
// gousb's InEndpoint.ReadContext returns context.DeadlineExceeded on a context
// deadline, which already implements Timeout() bool == true. Read passes that
// through unchanged.
func (t *ATTransport) Read(buf []byte) (int, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return 0, fmt.Errorf("usbtransport: read on closed transport")
	}
	t.mu.Unlock()

	rctx, cancel := context.WithTimeout(context.Background(), readPollInterval)
	defer cancel()
	return t.in.ReadContext(rctx, buf)
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

// Close releases the USB interface claim, configuration, and device. After
// Close, any blocked Read returns an error (its context is allowed to elapse,
// but the closed flag also gates further Read/Write). Subsequent Read/Write
// return an error immediately.
func (t *ATTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

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
	// Close the gousb context last, after all devices are released.
	if t.ctx != nil {
		if err := t.ctx.Close(); err != nil {
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
