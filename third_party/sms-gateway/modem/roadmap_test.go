package modem

import (
	"strings"
	"testing"

	"dji-modem-research/internal/testutil"
)

// Offline tests for the 11 vohive-sourced AT commands (roadmap Phase A-E).
// Uses the feedAfterDelay pattern (response fed after SendAndWait queues its call).

// --- Phase A: CIMI / CGMR ---

func TestIMSI(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "460011234567890\r\nOK\r\n")
	got, err := m.IMSI(testCtx())
	if err != nil {
		t.Fatalf("IMSI: %v", err)
	}
	if got != "460011234567890" {
		t.Errorf("IMSI = %q, want 460011234567890", got)
	}
	if w := string(port.Written()); !strings.Contains(w, "AT+CIMI") {
		t.Errorf("written %q", w)
	}
}

func TestSoftwareVersion(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "EC25EFAR08A04M4G\r\nOK\r\n")
	got, err := m.SoftwareVersion(testCtx())
	if err != nil {
		t.Fatalf("SoftwareVersion: %v", err)
	}
	if !strings.Contains(got, "EC25") {
		t.Errorf("SoftwareVersion = %q, want contains EC25", got)
	}
}

// --- Phase B: CREG / CGATT / CGDCONT / QNWINFO ---

func TestCSRegistration(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "+CREG: 2,5,\"ABCD\",\"1234\"\r\nOK\r\n")
	ri, err := m.CSRegistration(testCtx())
	if err != nil {
		t.Fatalf("CSRegistration: %v", err)
	}
	if !ri.Registered || !ri.Roaming {
		t.Errorf("stat=%d Registered=%v Roaming=%v, want roaming+registered", ri.Stat, ri.Registered, ri.Roaming)
	}
}

func TestPSAttached(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "+CGATT: 1\r\nOK\r\n")
	attached, err := m.PSAttached(testCtx())
	if err != nil {
		t.Fatalf("PSAttached: %v", err)
	}
	if !attached {
		t.Error("PSAttached = false, want true")
	}
}

func TestDefinePDP(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "OK\r\n")
	if err := m.DefinePDP(testCtx(), 1, "IP", "cmnet"); err != nil {
		t.Fatalf("DefinePDP: %v", err)
	}
	w := string(port.Written())
	if !strings.Contains(w, `AT+CGDCONT=1,"IP","cmnet"`) {
		t.Errorf("written %q", w)
	}
}

func TestListPDPs(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, `+CGDCONT: 1,"IP","cmnet","10.0.0.1",0,0`+"\r\nOK\r\n")
	pdps, err := m.ListPDPs(testCtx())
	if err != nil {
		t.Fatalf("ListPDPs: %v", err)
	}
	if len(pdps) != 1 {
		t.Fatalf("got %d PDPs, want 1", len(pdps))
	}
	p := pdps[0]
	if p.CID != 1 || p.Type != "IP" || p.APN != "cmnet" || p.Addr != "10.0.0.1" {
		t.Errorf("PDP = %+v", p)
	}
}

func TestQueryNetworkInfo(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, `+QNWINFO: "LTE","46000",3,4234`+"\r\nOK\r\n")
	ni, err := m.QueryNetworkInfo(testCtx())
	if err != nil {
		t.Fatalf("QueryNetworkInfo: %v", err)
	}
	if ni.Act != "LTE" || ni.Operator != "46000" || ni.Band != 3 || ni.Channel != 4234 {
		t.Errorf("NetworkInfo = %+v", ni)
	}
}

// --- Phase C: CFUN / QCFG ---

func TestSetFunctionLevel(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "OK\r\n")
	if err := m.SetFunctionLevel(testCtx(), 4, false); err != nil {
		t.Fatalf("SetFunctionLevel: %v", err)
	}
	w := string(port.Written())
	if !strings.Contains(w, "AT+CFUN=4") {
		t.Errorf("written %q, want AT+CFUN=4", w)
	}
}

func TestSetQCFG(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "OK\r\n")
	if err := m.SetQCFG(testCtx(), "urc/cache", "1"); err != nil {
		t.Fatalf("SetQCFG: %v", err)
	}
	w := string(port.Written())
	if !strings.Contains(w, `AT+QCFG="urc/cache",1`) {
		t.Errorf("written %q", w)
	}
}

// --- Phase D: CSIM / CRSM ---

func TestCSIM(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	// APDU 00A40400023F00 → response 3F000000 + SW 9000 = 6 bytes total
	feedAfterDelay(port, `+CSIM: 12,"3F0000009000"`+"\r\nOK\r\n")
	resp, err := m.CSIM(testCtx(), []byte{0x00, 0xA4, 0x04, 0x00, 0x02, 0x3F, 0x00})
	if err != nil {
		t.Fatalf("CSIM: %v", err)
	}
	if len(resp) != 6 {
		t.Errorf("resp len = %d, want 6", len(resp))
	}
	// last 2 bytes should be 90 00 (success SW)
	if len(resp) >= 2 && (resp[len(resp)-2] != 0x90 || resp[len(resp)-1] != 0x00) {
		t.Errorf("SW = % X, want 90 00", resp[len(resp)-2:])
	}
}

func TestReadSIMFile(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	// Read ICCID EF (0x2FE2): +CRSM: 144,0,"9860..."
	feedAfterDelay(port, `+CRSM: 144,0,"9860112233445566"`+"\r\nOK\r\n")
	data, err := m.ReadSIMFile(testCtx(), 0x2FE2, 0, 0, 10)
	if err != nil {
		t.Fatalf("ReadSIMFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("ReadSIMFile returned empty data")
	}
}

// --- Phase E: CUSD ---

func TestSendUSSD(t *testing.T) {
	port := testutil.NewScriptPort(nil)
	m := NewFromIO(port)
	defer m.Close()

	feedAfterDelay(port, "OK\r\n")
	if err := m.SendUSSD(testCtx(), "*100#"); err != nil {
		t.Fatalf("SendUSSD: %v", err)
	}
	w := string(port.Written())
	if !strings.Contains(w, `AT+CUSD=1,"*100#",15`) {
		t.Errorf("written %q", w)
	}
}
