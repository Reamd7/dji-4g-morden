package qmi

import (
	"context"
	"time"
)

// Transport exports the unexported qmiTransport interface so that external
// packages (e.g. internal/qmitransport) can implement it and inject a custom
// USB transport.
//
// The interface is:
//
//	Read([]byte) (int, error)          — blocking read of one QMUX frame
//	Write([]byte) (int, error)         — write one QMUX frame
//	Close() error                      — release the transport
//	SetReadDeadline(time.Time) error   — set a read timeout
type Transport = qmiTransport

// NewClientFromTransport creates a Client backed by a custom Transport (e.g. a
// user-space USB QMI transport using gousb control transfers) instead of
// opening /dev/cdc-wdm0.
//
// This is the injection point that lets the project bypass the Linux kernel
// cdc-wdm/qmi_wwan driver dependency: the caller provides a Transport that
// talks USB directly (model B: SEND_ENCAPSULATED_COMMAND + interrupt +
// GET_ENCAPSULATED_RESPONSE), and the QMI protocol stack runs entirely in
// user-space Go.
//
// The initialization mirrors NewClientWithOptions (client.go:230-302):
// newClientWithTransport starts the readLoop/writerLoop/indicationLoop
// goroutines, then SyncOnOpen sends QMICTL_SYNC to clear residual modem state
// from previous sessions. SYNC failure is non-fatal.
func NewClientFromTransport(ctx context.Context, conn Transport, opts ClientOptions) (*Client, error) {
	opts = normalizeClientOptions(opts)
	c := newClientWithTransport("usb", opts, conn)

	if opts.SyncOnOpen {
		syncCtx := ctx
		if syncCtx == nil {
			syncCtx = context.Background()
		}
		if _, hasDeadline := syncCtx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			syncCtx, cancel = context.WithTimeout(syncCtx, 5*time.Second)
			defer cancel()
		}
		if err := c.Sync(syncCtx); err != nil {
			c.logf(ClientLogLevelDebug, "QMI: initial sync failed (non-fatal): %v", err)
		}
	}

	return c, nil
}
