// Package qmitransport wraps the DJI Baiwang modem's QMI USB interface
// (MI_04, model B: EP0 control encapsulation) as a qmi.Transport, ready to
// feed into qmi.NewClientFromTransport.
//
// Model B transport (confirmed by Phase 0 probe, see
// plans/stage2/00-phase0-transport-probe.md):
//
//   - TX: dev.Control(0x21, 0x00, 0, iface, frame) — SEND_ENCAPSULATED_COMMAND
//   - RX: interrupt EP 0x89 (RESPONSE_AVAILABLE) → dev.Control(0xA1, 0x01, ...)
//     — GET_ENCAPSULATED_RESPONSE
//   - DTR: dev.Control(0x21, 0x22, 0x0001, iface, nil) — must be set before
//     any QMI communication (Linux qmi_wwan.c quirk_dtr)
//
// The interrupt endpoint is read by a background goroutine using a long-lived
// context cancelled only at Close — zero libusb_cancel_transfer during normal
// operation, avoiding the WinUSB segfault described in issue/001. This mirrors
// the AT transport's "direction F" pattern.
package qmitransport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/gousb"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

// USB constants for the DJI Baiwang QMI interface (MI_04, PID 0x0125).
const (
	// MI_04 interface number on the QDC507 module.
	DefaultIfaceNum = 4

	// EP 0x89 — interrupt IN, RESPONSE_AVAILABLE notifications.
	// maxPacketSize = 8 (see AGENTS.md endpoint table).
	DefaultIntrEP = 0x89

	// CDC class request types.
	bmReqClassOut  = 0x21 // class/interface/host→device
	bmReqClassIn   = 0xA1 // class/interface/device→host
	reqSendEncap   = 0x00 // SEND_ENCAPSULATED_COMMAND
	reqGetEncap    = 0x01 // GET_ENCAPSULATED_RESPONSE
	reqSetCtrlLine = 0x22 // SET_CONTROL_LINE_STATE
	dtrOn          = 0x0001

	// Delay after setting DTR to let the QMI service wake up (Phase 0: 500ms sufficient).
	dtrSettleDelay = 500 * time.Millisecond

	// Buffer for interrupt notification reads (EP 0x89 maxPacketSize = 8).
	intrBufSize = 8

	// Default USB vendor/product for the DJI Baiwang module.
	defaultVID = 0x2C7C
	defaultPID = 0x0125
)

// controlDevice abstracts gousb.Device.Control so the transport can be
// unit-tested with a mock (no real USB / no cgo).
// gousb's *gousb.Device satisfies this signature directly.
type controlDevice interface {
	Control(rType, request uint8, val, idx uint16, data []byte) (int, error)
}

// interruptReader abstracts the gousb IN endpoint so the interrupt read
// goroutine can be unit-tested.
// gousb's *gousb.InEndpoint satisfies this signature directly.
type interruptReader interface {
	ReadContext(ctx context.Context, buf []byte) (int, error)
}

// QMITransport wraps MI_04 as a qmi.Transport using EP0 control encapsulation
// (model B). It is the USB-side counterpart of Linux's /dev/cdc-wdm0.
//
// A background goroutine reads the interrupt endpoint (EP 0x89) with a
// long-lived context, signalling notifyCh when RESPONSE_AVAILABLE arrives.
// Read() waits on notifyCh (with the deadline from SetReadDeadline), then
// issues a GET_ENCAPSULATED_RESPONSE control transfer to fetch the QMUX frame.
type QMITransport struct {
	ctx      *gousb.Context
	dev      *gousb.Device
	cfg      *gousb.Config
	iface    *gousb.Interface
	ifaceNum int

	// Abstracted USB operations (for testability).
	ctrl controlDevice // SEND/GET/DTR control transfers
	intr interruptReader // EP 0x89 interrupt IN

	// Long-lived context for the interrupt goroutine. Cancelled only at Close
	// (direction F: zero cancel_transfer during normal operation, issue/001 safe).
	readCtx    context.Context
	readCancel context.CancelFunc

	// notifyCh is signalled by the interrupt goroutine when RESPONSE_AVAILABLE
	// is received. Buffered(1) so a notification arriving between two Read()
	// calls (e.g. after a deadline timeout) is not lost.
	notifyCh chan struct{}

	// intrDone is closed when the interrupt goroutine exits, so Close can wait.
	intrDone chan struct{}

	// deadline is set by SetReadDeadline and checked by Read. Both are called
	// sequentially by readLoop (single goroutine), so no concurrent access —
	// deadlineMu is kept for defensive safety.
	deadlineMu sync.Mutex
	deadline   time.Time

	// ioMu serializes ALL USB control transfers (Read's GET, Write's SEND,
	// Close's DTR-clear + handle-release) and protects the closed flag.
	//
	// This is the core concurrency guard for model B. gousb v1.1.3 has no
	// Device.ControlContext — control transfers block with no context cancel.
	// Close MUST acquire ioMu before releasing USB handles to guarantee no
	// in-flight dev.Control is left dangling. Without this, the QMI client's
	// Close order (conn.Close() at client.go:336 BEFORE wg.Wait() at :337)
	// would release handles while writerLoop's Write or readLoop's GET is
	// still executing → segfault (issue/001 class).
	//
	// Lock ordering: ioMu is the only USB-serializing mutex. deadlineMu is
	// never held simultaneously with ioMu. No deadlock possible.
	ioMu   sync.Mutex
	closed bool
}

// Open claims MI_04 on the DJI Baiwang module (VID 0x2C7C, PID 0x0125),
// sets DTR, and starts the interrupt read goroutine.
//
// The returned QMITransport must be Close()d to release the USB interface
// claim and stop the goroutine.
func Open() (*QMITransport, error) {
	return OpenWithVIDPID(defaultVID, defaultPID)
}

// OpenWithVIDPID is like Open but allows specifying the USB vendor/product ID.
// For the DJI Baiwang module: vid=0x2C7C, pid=0x0125.
func OpenWithVIDPID(vid, pid uint16) (*QMITransport, error) {
	return openInternal(vid, pid, DefaultIfaceNum, DefaultIntrEP)
}

// openInternal is the core Open logic, split out for testability.
// It opens the device, claims the interface, sets DTR, and starts the
// interrupt goroutine.
func openInternal(vid, pid uint16, ifaceNum, intrEP int) (*QMITransport, error) {
	ctx := gousb.NewContext()
	dev, err := ctx.OpenDeviceWithVIDPID(gousb.ID(vid), gousb.ID(pid))
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("qmitransport: open %04x:%04x: %w", vid, pid, err)
	}
	if dev == nil {
		ctx.Close()
		return nil, fmt.Errorf("qmitransport: device %04x:%04x not found", vid, pid)
	}

	cfg, err := dev.Config(1)
	if err != nil {
		dev.Close()
		ctx.Close()
		return nil, fmt.Errorf("qmitransport: config 1: %w", err)
	}

	iface, err := cfg.Interface(ifaceNum, 0)
	if err != nil {
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, fmt.Errorf("qmitransport: claim interface %d: %w", ifaceNum, err)
	}

	intrIn, err := iface.InEndpoint(intrEP)
	if err != nil {
		iface.Close()
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, fmt.Errorf("qmitransport: interrupt IN endpoint 0x%02x: %w", intrEP, err)
	}

	// Set DTR — the QDC507 will not respond to QMI requests until DTR is set.
	// Discovered from Linux qmi_wwan.c: QMI_MATCH_FF_FF_FF(0x2c7c,0x0125) → quirk_dtr.
	if _, err := dev.Control(bmReqClassOut, reqSetCtrlLine, dtrOn, uint16(ifaceNum), nil); err != nil {
		iface.Close()
		cfg.Close()
		dev.Close()
		ctx.Close()
		return nil, fmt.Errorf("qmitransport: set DTR failed: %w", err)
	}
	time.Sleep(dtrSettleDelay) // let QMI service wake up

	readCtx, readCancel := context.WithCancel(context.Background())
	t := &QMITransport{
		ctx:       ctx,
		dev:       dev,
		cfg:       cfg,
		iface:     iface,
		ifaceNum:  ifaceNum,
		ctrl:      dev,
		intr:      intrIn,
		readCtx:   readCtx,
		readCancel: readCancel,
		notifyCh:  make(chan struct{}, 1),
		intrDone:  make(chan struct{}),
	}

	// Start the background interrupt reader goroutine.
	go t.interruptLoop()

	return t, nil
}

// interruptLoop reads the interrupt endpoint (EP 0x89) in a long-blocking
// fashion. When RESPONSE_AVAILABLE is received, it signals notifyCh.
//
// This goroutine runs for the lifetime of the transport. The readCtx is
// cancelled only at Close, so the interrupt ReadContext transfer stays
// submitted (zero cancel_transfer) during normal operation — this avoids
// the WinUSB segfault described in issue/001.
func (t *QMITransport) interruptLoop() {
	defer close(t.intrDone)
	buf := make([]byte, intrBufSize)

	for {
		n, err := t.intr.ReadContext(t.readCtx, buf)
		if err != nil {
			// Context cancelled (Close) or USB error — either way, exit.
			return
		}
		if n == 0 {
			continue
		}

		// RESPONSE_AVAILABLE notification received. Signal Read().
		// If Read() isn't waiting (e.g. between calls), the buffered channel
		// holds the notification until the next Read().
		select {
		case t.notifyCh <- struct{}{}:
		case <-t.readCtx.Done():
			return
		}
	}
}

// Read implements qmi.Transport.Read. It blocks until a QMUX frame is
// available (interrupt notification + control GET) or the read deadline
// expires.
//
// The deadline is set by the QMI client's readLoop via SetReadDeadline
// (100ms with DefaultClientOptions). On deadline expiry, Read returns a
// timeout error so readLoop can check its close channel and loop.
//
// Concurrency: the control GET is protected by ioMu. If Close acquires
// ioMu first (setting closed=true and releasing handles), Read will see
// closed=true once it gets the lock and return without touching USB.
func (t *QMITransport) Read(buf []byte) (int, error) {
	// Read the deadline set by SetReadDeadline.
	t.deadlineMu.Lock()
	dl := t.deadline
	t.deadlineMu.Unlock()

	var timerC <-chan time.Time
	if !dl.IsZero() {
		d := time.Until(dl)
		if d <= 0 {
			return 0, errTimeout{}
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case <-t.notifyCh:
		// RESPONSE_AVAILABLE — fetch the QMUX frame via control GET.
		t.ioMu.Lock()
		defer t.ioMu.Unlock()
		if t.closed {
			return 0, errClosed
		}
		n, err := t.ctrl.Control(bmReqClassIn, reqGetEncap, 0x0000, uint16(t.ifaceNum), buf)
		if err != nil {
			return n, fmt.Errorf("qmitransport: GET_ENCAPSULATED_RESPONSE: %w", err)
		}
		return n, nil

	case <-timerC:
		return 0, errTimeout{}

	case <-t.readCtx.Done():
		return 0, errClosed
	}
}

// Write implements qmi.Transport.Write. It sends a QMUX frame via
// SEND_ENCAPSULATED_COMMAND control transfer.
func (t *QMITransport) Write(buf []byte) (int, error) {
	t.ioMu.Lock()
	defer t.ioMu.Unlock()
	if t.closed {
		return 0, errClosed
	}
	n, err := t.ctrl.Control(bmReqClassOut, reqSendEncap, 0x0000, uint16(t.ifaceNum), buf)
	if err != nil {
		return n, fmt.Errorf("qmitransport: SEND_ENCAPSULATED_COMMAND: %w", err)
	}
	return n, nil
}

// SetReadDeadline sets the deadline for the next Read call. A zero time
// means no deadline (block indefinitely).
//
// The QMI client's readLoop calls this before each Read (100ms with
// DefaultClientOptions). The deadline is enforced purely in Go (timer +
// channel select), NOT at the USB level — the interrupt endpoint stays
// submitted with zero cancel_transfer.
func (t *QMITransport) SetReadDeadline(t2 time.Time) error {
	t.deadlineMu.Lock()
	t.deadline = t2
	t.deadlineMu.Unlock()
	return nil
}

// Close releases the USB interface, stops the interrupt goroutine, and
// clears DTR.
//
// Concurrency model (issue/001 hardening for model B):
//
// The QMI client calls conn.Close() (client.go:336) BEFORE wg.Wait() (:337),
// so writerLoop and readLoop may still be in-flight when Close runs. Since
// gousb v1.1.3 has no Device.ControlContext, control transfers (Read's GET,
// Write's SEND) cannot be cancelled by context — Close MUST wait for them
// to finish before releasing USB handles.
//
// Solution: Close holds ioMu for its entire duration. Any in-flight Read
// (holding ioMu during GET) or Write (holding ioMu during SEND) blocks Close
// until they complete. Once Close gets ioMu, it sets closed=true (preventing
// new operations), stops the interrupt goroutine, and safely releases handles.
//
// The interrupt goroutine does NOT use ioMu (it only does intrIn.ReadContext
// + channel send), so readCancel + <-intrDone proceed without deadlock while
// Close holds ioMu.
func (t *QMITransport) Close() error {
	t.ioMu.Lock()
	defer t.ioMu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	// Stop the interrupt goroutine. Safe while holding ioMu: the goroutine
	// doesn't use ioMu — it only does intrIn.ReadContext + channel send.
	if t.readCancel != nil {
		t.readCancel()
	}
	<-t.intrDone

	// All USB control transfers are now guaranteed idle (ioMu held).
	// Clear DTR (best-effort).
	if t.dev != nil {
		_, _ = t.dev.Control(bmReqClassOut, reqSetCtrlLine, 0x0000, uint16(t.ifaceNum), nil)
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
	if t.ctx != nil {
		if err := t.ctx.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 1 {
		return errs[0]
	}
	if len(errs) > 1 {
		return fmt.Errorf("qmitransport: close errors: %v", errs)
	}
	return nil
}

// errClosed is returned by Read/Write when the transport has been Closed.
var errClosed = fmt.Errorf("qmitransport: transport closed")

// errTimeout is returned by Read when the deadline expires. It implements
// the timeout interface so os.IsTimeout returns true, which the QMI client's
// readLoop checks to decide whether to loop (timeout) or abort (real error).
type errTimeout struct{}

func (errTimeout) Error() string   { return "qmitransport: read deadline exceeded" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return true }

// Compile-time interface checks.
var (
	_ controlDevice   = (*gousb.Device)(nil)
	_ interruptReader = (*gousb.InEndpoint)(nil)

	// QMITransport satisfies the qmi.Transport interface exported by
	// third_party/quectel-qmi-go/qmi/transport_export.go.
	_ qmi.Transport = (*QMITransport)(nil)
)
