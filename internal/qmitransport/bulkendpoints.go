package qmitransport

import (
	"fmt"

	"github.com/google/gousb"
)

// MI_04 bulk endpoints for raw IP data transfer (see AGENTS.md endpoint table).
//
// After WDA SetDataFormat(LinkProtocolIP) + WDS StartNetwork, the modem
// sends/receives raw IP packets (no ethernet header, no QMAP wrapper) on
// these endpoints. They share the same claimed MI_04 interface as the QMI
// control path (EP0 + interrupt 0x89) — no additional USB claim needed.
const (
	// DefaultBulkInEP is EP 0x88 — bulk IN, raw IP from modem.
	// maxPacketSize = 512 (USB 2.0 high-speed).
	DefaultBulkInEP = 0x88

	// DefaultBulkOutEP is EP 0x05 — bulk OUT, raw IP to modem.
	// maxPacketSize = 512 (USB 2.0 high-speed).
	DefaultBulkOutEP = 0x05
)

// OpenBulkEndpoints opens MI_04's bulk IN (EP 0x88) and bulk OUT (EP 0x05)
// for raw IP data transfer. Must be called after Open(). The endpoints are
// on the same claimed interface as the control path (EP0 + interrupt 0x89) —
// no additional USB claim needed; bulk and control use different endpoints
// with no contention.
//
// The returned endpoints operate independently of QMITransport's ioMu (which
// only serializes EP0 control transfers). Callers MUST stop using these
// endpoints before Close() — Close releases the underlying interface.
func (t *QMITransport) OpenBulkEndpoints() (bulkIn *gousb.InEndpoint, bulkOut *gousb.OutEndpoint, err error) {
	t.ioMu.Lock()
	defer t.ioMu.Unlock()

	if t.closed {
		return nil, nil, fmt.Errorf("qmitransport: transport closed")
	}

	bulkIn, err = t.iface.InEndpoint(DefaultBulkInEP)
	if err != nil {
		return nil, nil, fmt.Errorf("qmitransport: bulk IN endpoint 0x%02x: %w", DefaultBulkInEP, err)
	}

	bulkOut, err = t.iface.OutEndpoint(DefaultBulkOutEP)
	if err != nil {
		return nil, nil, fmt.Errorf("qmitransport: bulk OUT endpoint 0x%02x: %w", DefaultBulkOutEP, err)
	}

	return bulkIn, bulkOut, nil
}
