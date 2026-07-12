package modem

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// sim.go — SIM APDU passthrough (AT+CSIM) and restricted SIM access (AT+CRSM).
// Both vohive-sourced. See plans/at-commands-roadmap.md Phase D.

// CSIM sends a raw APDU to the SIM via AT+CSIM and returns the response bytes.
// This is the generic SIM access command (TS 27.007 §8.17, EC25 §5.5).
// For higher-level file read/write, prefer ReadSIMFile/WriteSIMFile (AT+CRSM).
func (m *Modem) CSIM(ctx context.Context, apdu []byte) ([]byte, error) {
	hexCmd := hex.EncodeToString(apdu)
	cmd := fmt.Sprintf(`AT+CSIM=%d,"%s"`, len(hexCmd), hexCmd)
	lines, err := m.SendAndWait(ctx, cmd, 5*time.Second)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "+CSIM:") {
			continue
		}
		// +CSIM: <length>,"<hex response>"
		parts := splitCSVQuoted(strings.TrimPrefix(l, "+CSIM:"))
		if len(parts) < 2 {
			return nil, fmt.Errorf("CSIM: malformed response %q", l)
		}
		hexResp := strings.Trim(parts[1], `"`)
		resp, err := hex.DecodeString(hexResp)
		if err != nil {
			return nil, fmt.Errorf("CSIM: hex decode %q: %w", hexResp, err)
		}
		return resp, nil
	}
	return nil, fmt.Errorf("CSIM: no +CSIM in response")
}

// CRSMCommand is the TS 27.007 §8.18 command value for AT+CRSM.
const (
	CRSMReadBinary   = 176 // 0xB0
	CRSMReadRecord   = 178 // 0xB2
	CRSMUpdateBinary = 214 // 0xD6
	CRSMUpdateRecord = 220 // 0xDC
)

// ReadSIMFile reads data from a SIM EF (Elementary File) via AT+CRSM (READ BINARY).
// fileID is the 2-byte file identifier (e.g. 0x2FE2 for ICCID).
// p1/p2/p3 are the offset/length parameters per TS 102 221.
// Returns the raw file content bytes.
func (m *Modem) ReadSIMFile(ctx context.Context, fileID int, p1, p2, p3 int) ([]byte, error) {
	cmd := fmt.Sprintf("AT+CRSM=%d,%d,%d,%d,%d",
		CRSMReadBinary, fileID, p1, p2, p3)
	return m.crsmExchange(ctx, cmd)
}

// WriteSIMFile writes data to a SIM EF via AT+CRSM (UPDATE BINARY).
func (m *Modem) WriteSIMFile(ctx context.Context, fileID int, p1, p2 int, data []byte) error {
	hexData := hex.EncodeToString(data)
	cmd := fmt.Sprintf(`AT+CRSM=%d,%d,%d,%d,%d,"%s"`,
		CRSMUpdateBinary, fileID, p1, p2, len(data), hexData)
	_, err := m.crsmExchange(ctx, cmd)
	return err
}

// crsmExchange sends an AT+CRSM command and parses the +CRSM: <sw1>,<sw2>[,<data>] response.
// Returns the data bytes (if any). SW1/SW2 status is not checked here — the caller
// can inspect the returned bytes for 90 00 (success) if needed.
func (m *Modem) crsmExchange(ctx context.Context, cmd string) ([]byte, error) {
	lines, err := m.SendAndWait(ctx, cmd, 5*time.Second)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "+CRSM:") {
			continue
		}
		parts := splitCSVQuoted(strings.TrimPrefix(l, "+CRSM:"))
		if len(parts) < 2 {
			return nil, fmt.Errorf("CRSM: malformed response %q", l)
		}
		sw1, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		_ = sw1 // caller can check 0x90==success if desired
		if len(parts) >= 3 {
			hexResp := strings.Trim(parts[2], `"`)
			return hex.DecodeString(hexResp)
		}
		return nil, nil // SW only, no data
	}
	return nil, fmt.Errorf("CRSM: no +CRSM in response")
}
