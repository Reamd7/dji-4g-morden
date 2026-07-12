package modem

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// network.go — Network registration / PDP status AT commands.
// All vohive-sourced (SG doesn't have these). See plans/at-commands-roadmap.md Phase B.

// RegInfo holds a simplified network registration result (CS or PS).
type RegInfo struct {
	Registered bool   // true if stat is home(1) or roaming(5)
	Roaming    bool   // true if stat is roaming(5)
	Stat       int    // raw <stat> value
	LAC        string // Location Area Code (hex, optional)
	CellID     string // Cell ID (hex, optional)
}

// CSRegistration queries the CS (Circuit Switched) network registration status
// (AT+CREG?). See EC25 AT Commands Manual §6.2.
func (m *Modem) CSRegistration(ctx context.Context) (RegInfo, error) {
	lines, err := m.SendAndWait(ctx, "AT+CREG?", 3*time.Second)
	if err != nil {
		return RegInfo{}, err
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "+CREG:") {
			continue
		}
		return parseRegInfo(strings.TrimPrefix(l, "+CREG:")), nil
	}
	return RegInfo{}, fmt.Errorf("CREG: no +CREG in response")
}

// parseRegInfo parses "<n>,<stat>[,<lac>,<ci>]" into RegInfo.
func parseRegInfo(body string) RegInfo {
	parts := splitCSVQuoted(strings.TrimSpace(body))
	var ri RegInfo
	if len(parts) >= 2 {
		ri.Stat, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		ri.Registered = ri.Stat == 1 || ri.Stat == 5
		ri.Roaming = ri.Stat == 5
	}
	if len(parts) >= 3 {
		ri.LAC = strings.TrimSpace(parts[2])
	}
	if len(parts) >= 4 {
		ri.CellID = strings.TrimSpace(parts[3])
	}
	return ri
}

// PSAttached queries whether the modem is PS (Packet Switched) attached
// (AT+CGATT?). Returns true if attached. See EC25 AT Commands Manual §10.1.
func (m *Modem) PSAttached(ctx context.Context) (bool, error) {
	lines, err := m.SendAndWait(ctx, "AT+CGATT?", 3*time.Second)
	if err != nil {
		return false, err
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "+CGATT:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(l, "+CGATT:"))
		return val == "1", nil
	}
	return false, fmt.Errorf("CGATT: no +CGATT in response")
}

// SetPSAttach attaches to or detaches from the PS domain (AT+CGATT=<state>).
// See EC25 AT Commands Manual §10.1.
func (m *Modem) SetPSAttach(ctx context.Context, attached bool) error {
	v := 0
	if attached {
		v = 1
	}
	_, err := m.SendAndWait(ctx, fmt.Sprintf("AT+CGATT=%d", v), 15*time.Second)
	return err
}

// PDPContext describes one PDP context entry (AT+CGDCONT?).
type PDPContext struct {
	CID  int
	Type string // "IP", "IPV6", "IPV4V6"
	APN  string
	Addr string // assigned IP (empty if not activated)
}

// DefinePDP defines or overwrites a PDP context (AT+CGDCONT=<cid>,<type>,<APN>).
// See EC25 AT Commands Manual §10.2.
func (m *Modem) DefinePDP(ctx context.Context, cid int, pdpType, apn string) error {
	_, err := m.SendAndWait(ctx,
		fmt.Sprintf(`AT+CGDCONT=%d,"%s","%s"`, cid, pdpType, apn), 5*time.Second)
	return err
}

// ListPDPs queries all defined PDP contexts (AT+CGDCONT?).
// See EC25 AT Commands Manual §10.2.
func (m *Modem) ListPDPs(ctx context.Context) ([]PDPContext, error) {
	lines, err := m.SendAndWait(ctx, "AT+CGDCONT?", 5*time.Second)
	if err != nil {
		return nil, err
	}
	var out []PDPContext
	for _, l := range lines {
		if !strings.HasPrefix(l, "+CGDCONT:") {
			continue
		}
		parts := splitCSVQuoted(strings.TrimSpace(strings.TrimPrefix(l, "+CGDCONT:")))
		if len(parts) < 2 {
			continue
		}
		var p PDPContext
		p.CID, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
		if len(parts) > 1 {
			p.Type = strings.Trim(parts[1], `"`)
		}
		if len(parts) > 2 {
			p.APN = strings.Trim(parts[2], `"`)
		}
		if len(parts) > 3 {
			p.Addr = strings.Trim(parts[3], `"`)
		}
		out = append(out, p)
	}
	return out, nil
}

// NetworkInfo holds the current serving cell's radio parameters (AT+QNWINFO).
type NetworkInfo struct {
	Act      string // "LTE", "WCDMA", "GSM", ...
	Operator string // PLMN numeric
	Band     int
	Channel  int
}

// NetworkInfo queries the current network's access technology, operator, band
// and channel (AT+QNWINFO). See EC25 AT Commands Manual §6.9.
func (m *Modem) QueryNetworkInfo(ctx context.Context) (NetworkInfo, error) {
	lines, err := m.SendAndWait(ctx, "AT+QNWINFO", 3*time.Second)
	if err != nil {
		return NetworkInfo{}, err
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "+QNWINFO:") {
			continue
		}
		parts := splitCSVQuoted(strings.TrimSpace(strings.TrimPrefix(l, "+QNWINFO:")))
		var ni NetworkInfo
		if len(parts) > 0 {
			ni.Act = strings.Trim(parts[0], `"`)
		}
		if len(parts) > 1 {
			ni.Operator = strings.Trim(parts[1], `"`)
		}
		if len(parts) > 2 {
			ni.Band, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
		}
		if len(parts) > 3 {
			ni.Channel, _ = strconv.Atoi(strings.TrimSpace(parts[3]))
		}
		return ni, nil
	}
	return NetworkInfo{}, fmt.Errorf("QNWINFO: no +QNWINFO in response")
}
