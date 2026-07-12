package modem

import (
	"context"
	"strings"
	"testing"
	"time"

	"dji-modem-research/internal/testutil"
)

// testCtx returns a context with a generous timeout for offline tests.
func testCtx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cancel
	return ctx
}

// --- parseCMTI (pure logic) ---

func TestParseCMTI(t *testing.T) {
	cases := []struct {
		line    string
		wantIdx int
		wantMem string
	}{
		{`+CMTI: "SM",3`, 3, "SM"},
		{`+CMTI: "ME",15`, 15, "ME"},
		{`+CMTI: "SM",0`, 0, "SM"},
		{`+CMTI:garbage`, 0, ""},
		{`not-cmti`, 0, ""},
	}
	for _, tc := range cases {
		idx, mem := parseCMTI(tc.line)
		if idx != tc.wantIdx || mem != tc.wantMem {
			t.Errorf("parseCMTI(%q) = (%d, %q), want (%d, %q)", tc.line, idx, mem, tc.wantIdx, tc.wantMem)
		}
	}
}

// --- B-class command wrappers via ScriptPort mock ---
//
// readerLoop uses a long-lived blocking read (direction F), so response bytes
// fed before a command's pending call is queued get mis-dispatched as URCs.
// feedAfterDelay runs Feed in a goroutine that lands the response shortly
// AFTER the caller's SendAndWait has queued its pending call.

func feedAfterDelay(port *testutil.ScriptPort, resp string) {
	go func() {
		time.Sleep(80 * time.Millisecond)
		port.Feed([]byte(resp))
	}()
}

func TestSMSCQueriesServiceCenter(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "+CSCA: \"+8613800100500\",145\r\nOK\r\n")
	smsc, err := m.SMSC(testCtx())
	if err != nil {
		t.Fatalf("SMSC: %v", err)
	}
	if smsc != "+8613800100500" {
		t.Errorf("SMSC = %q, want +8613800100500", smsc)
	}
	if w := string(port.Written()); !strings.Contains(w, "AT+CSCA?") {
		t.Errorf("written %q, want AT+CSCA?", w)
	}
}

func TestReadStoredParsesPDU(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "+CMGR: 0,,28\r\n0891683110200005F0040D91683100858821F60000\r\nOK\r\n")
	sm, err := m.ReadStored(testCtx(), 5)
	if err != nil {
		t.Fatalf("ReadStored: %v", err)
	}
	if sm.Index != 5 {
		t.Errorf("Index = %d, want 5", sm.Index)
	}
	if !strings.HasPrefix(sm.PDU, "0891") {
		t.Errorf("PDU = %q, want hex starting with 0891", sm.PDU)
	}
}

func TestDeleteAllStoredSendsCMGD14(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "OK\r\n")
	if err := m.DeleteAllStored(testCtx()); err != nil {
		t.Fatalf("DeleteAllStored: %v", err)
	}
	if w := string(port.Written()); !strings.Contains(w, "AT+CMGD=1,4") {
		t.Errorf("written %q, want AT+CMGD=1,4", w)
	}
}

func TestSetCharsetSendsCSCS(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "OK\r\n")
	if err := m.SetCharset(testCtx(), "UCS2"); err != nil {
		t.Fatalf("SetCharset: %v", err)
	}
	if w := string(port.Written()); !strings.Contains(w, `AT+CSCS="UCS2"`) {
		t.Errorf("written %q, want AT+CSCS=\"UCS2\"", w)
	}
}
