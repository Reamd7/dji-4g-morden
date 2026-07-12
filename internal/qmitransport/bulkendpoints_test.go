package qmitransport

import (
	"testing"
)

// TestOpenBulkEndpointsAfterClose verifies that OpenBulkEndpoints returns an
// error when the transport is closed. The actual endpoint opening
// (iface.InEndpoint/OutEndpoint) requires a real gousb.Interface and is
// covered by hardware integration tests.
func TestOpenBulkEndpointsAfterClose(t *testing.T) {
	ctrl := &mockControlDevice{}
	intr := &mockInterruptReader{notifyCh: make(chan struct{})}
	tr := newTestTransport(ctrl, intr)

	tr.Close()

	bulkIn, bulkOut, err := tr.OpenBulkEndpoints()
	if err == nil {
		t.Fatal("OpenBulkEndpoints after Close should return error")
	}
	if bulkIn != nil {
		t.Fatal("bulkIn should be nil on error")
	}
	if bulkOut != nil {
		t.Fatal("bulkOut should be nil on error")
	}
}

// TestOpenBulkEndpointsConstants verifies the endpoint address constants
// match the known DJI Baiwang MI_04 layout (EP 0x88 IN / 0x05 OUT).
func TestOpenBulkEndpointsConstants(t *testing.T) {
	if DefaultBulkInEP != 0x88 {
		t.Errorf("DefaultBulkInEP = 0x%02x, want 0x88", DefaultBulkInEP)
	}
	if DefaultBulkOutEP != 0x05 {
		t.Errorf("DefaultBulkOutEP = 0x%02x, want 0x05", DefaultBulkOutEP)
	}
}
