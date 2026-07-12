//go:build hardware

// Hardware integration tests for usbtransport. These require a real DJI
// Baiwang modem (Quectel EC25 PID 2C7C:0125) plugged in with WinUSB drivers
// installed via Zadig on MI_00..MI_04. Run with:
//
//	go test -tags=hardware ./internal/usbtransport/
//
// They will not run under the default `go test ./...` (CI) because the
// `hardware` build tag is unset.
package usbtransport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	modem "dji-modem-research/third_party/sms-gateway/modem"
)

// isTimeoutErr reports whether err is a context-cancellation error. Since
// direction F (long-lived read), Read returns context.Canceled on Close,
// which implements Timeout() bool == true.
func isTimeoutErr(err error) bool {
	var t interface{ Timeout() bool }
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return false
}

// EC25 PID 0125 AT command port (MI_02). See AGENTS.md "实测验证结果".
const (
	hwVID      = 0x2C7C
	hwPID      = 0x0125
	hwIfaceAT  = 2
	hwEpOut    = 0x03
	hwEpIn     = 0x84
)

// TestHardwareATPing proves the transport carries a raw AT command and reads
// OK back — the same smoke test as cmd/attest but through the ATTransport
// abstraction.
func TestHardwareATPing(t *testing.T) {
	tt, err := Open(hwVID, hwPID, hwIfaceAT, hwEpOut, hwEpIn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tt.Close()

	// Send AT, expect OK.
	if _, err := tt.Write([]byte("AT\r\n")); err != nil {
		t.Fatalf("Write AT: %v", err)
	}
	// Read until we see OK or time out.
	buf := make([]byte, 512)
	deadline := time.Now().Add(5 * time.Second)
	got := strings.Builder{}
	for time.Now().Before(deadline) {
		n, err := tt.Read(buf)
		if n > 0 {
			got.Write(buf[:n])
			if strings.Contains(got.String(), "OK") {
				return // success
			}
		}
		if err != nil && !isTimeoutErr(err) {
			t.Fatalf("Read: %v", err)
		}
	}
	t.Fatalf("AT did not return OK within 5s; got %q", got.String())
}

// TestHardwareModemInitializeAndCSQ is the full end-to-end milestone: USB
// transport → modem.NewFromIO → modem.Initialize (ATE0/CMEE=1/CPIN?/CMGF=0/
// CNMI/CPMS) → SendAndWait("AT+CSQ"). Success proves the transport drives
// the complete sms_gateway/modem AT protocol layer over real USB.
func TestHardwareModemInitializeAndCSQ(t *testing.T) {
	tt, err := Open(hwVID, hwPID, hwIfaceAT, hwEpOut, hwEpIn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tt.Close()

	m := modem.NewFromIO(tt)
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := m.Initialize(ctx, ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	lines, err := m.SendAndWait(ctx, "AT+CSQ", 5*time.Second)
	if err != nil {
		t.Fatalf("SendAndWait AT+CSQ: %v", err)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "+CSQ:") {
			t.Logf("signal: %s", l)
			return
		}
	}
	t.Errorf("AT+CSQ returned no +CSQ line: %v", lines)
}
