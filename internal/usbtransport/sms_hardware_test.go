//go:build hardware

// SMS hardware integration tests. Like usbtransport_hardware_test.go, these
// require a real DJI Baiwang modem (EC25 PID 2C7C:0125) with WinUSB on MI_02.
//
//	go test -tags=hardware -v ./internal/usbtransport/ -run TestHardwareSMS
//
// Read-only tests (ListStored, ICCID, IMEI, signal) run unconditionally.
// TestHardwareSMSSend is skipped unless DJI_TEST_SMS_RECIPIENT is set, since
// sending a real SMS costs money / disturbs a real subscriber.
package usbtransport

import (
	"context"
	"os"
	"testing"
	"time"

	modem "dji-modem-research/third_party/sms-gateway/modem"
)

// openInitializedModem is a shared helper: open the AT transport, build a
// Modem, run Initialize, returning both for use in the test. Caller closes.
func openInitializedModem(t *testing.T) (*ATTransport, *modem.Modem) {
	t.Helper()
	tt, err := Open(hwVID, hwPID, hwIfaceAT, hwEpOut, hwEpIn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m := modem.NewFromIO(tt)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Initialize(ctx, ""); err != nil {
		tt.Close()
		t.Fatalf("Initialize: %v", err)
	}
	return tt, m
}

// TestHardwareDeviceInfo queries SIM/card identity plus signal. All read-only.
// Verifies the AT transport carries the full range of +CXXX query commands,
// not just +CSQ.
func TestHardwareDeviceInfo(t *testing.T) {
	tt, m := openInitializedModem(t)
	defer m.Close()
	defer tt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	iccid := m.ICCID(ctx)
	t.Logf("ICCID: %s", iccid)
	if iccid == "" {
		t.Error("ICCID empty")
	}

	imei := m.IMEI(ctx)
	t.Logf("IMEI: %s", imei)
	if imei == "" {
		t.Error("IMEI empty")
	}

	carrier := m.Carrier(ctx)
	t.Logf("Carrier: %s", carrier)

	msisdn := m.PhoneNumber(ctx)
	t.Logf("PhoneNumber (CNUM): %s", msisdn) // may be empty — SIM doesn't always store its own MSISDN

	rssi, ok := m.SignalDBm(ctx)
	t.Logf("SignalDBm: rssi=%d ok=%v", rssi, ok)
}

// TestHardwareSMSListStoredAndDecode reads stored SMS from SIM storage and
// decodes each via DecodeDeliver. Read-only — does not send or delete.
//
// The earlier Initialize showed "+CPMS: 3,50" (3 messages stored), so this
// test expects to find ≥1 stored message and decode it without error.
func TestHardwareSMSListStoredAndDecode(t *testing.T) {
	tt, m := openInitializedModem(t)
	defer m.Close()
	defer tt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msgs, err := m.ListStored(ctx)
	if err != nil {
		t.Fatalf("ListStored: %v", err)
	}
	t.Logf("stored message count: %d", len(msgs))

	if len(msgs) == 0 {
		t.Skip("no stored messages on SIM — send a test SMS to this card first")
	}

	for _, sm := range msgs {
		decoded, err := modem.DecodeDeliver(sm.PDU)
		if err != nil {
			t.Errorf("index %d PDU %q: DecodeDeliver: %v", sm.Index, sm.PDU, err)
			continue
		}
		t.Logf("  [%d] from=%s time=%s body=%q concat=%v",
			sm.Index, decoded.Sender, decoded.Timestamp.Format(time.RFC3339),
			decoded.Body, decoded.Concat)
		// Basic sanity on the decoded fields.
		if decoded.Sender == "" {
			t.Errorf("index %d: empty sender", sm.Index)
		}
	}
}

// TestHardwareSMSSend sends a real SMS to the number in DJI_TEST_SMS_RECIPIENT.
// Skipped unless that env var is set, since it costs money and notifies a real
// subscriber. The body is a short ASCII marker so the recipient can identify
// it. Send uses the full two-step CMGS handshake (prompt ">" + PDU + Ctrl-Z),
// so success proves the AT transport correctly carries that interactive flow.
func TestHardwareSMSSend(t *testing.T) {
	recipient := os.Getenv("DJI_TEST_SMS_RECIPIENT")
	if recipient == "" {
		t.Skip("DJI_TEST_SMS_RECIPIENT not set — skipping send test")
	}

	tt, m := openInitializedModem(t)
	defer m.Close()
	defer tt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	body := "dji-modem-research USB transport test"
	if err := m.Send(ctx, recipient, body, modem.SubmitUDH{}); err != nil {
		t.Fatalf("Send to %s: %v", recipient, err)
	}
	t.Logf("SMS sent to %s: %q", recipient, body)
}
