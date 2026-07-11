// modem/sms.go — high-level SMS operations layered over Modem AT transport.

package modem

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Initialize the modem for SMS work in PDU mode. Idempotent.
//
//   - AT             : ping
//   - ATE0           : disable echo (we strip it anyway, but it speeds parsing)
//   - AT+CMEE=1      : numeric extended errors so failures are unambiguous
//   - AT+CPIN?       : SIM ready check; PIN provided if needed
//   - AT+CMGF=0      : PDU mode
//   - AT+CNMI=2,1    : new SMS deposited in storage, URC notified
//   - AT+CPMS="SM"   : SIM storage for read/write/delete
func (m *Modem) Initialize(ctx context.Context, pin string) error {
	if _, err := m.SendAndWait(ctx, "AT", 2*time.Second); err != nil {
		return fmt.Errorf("AT ping: %w", err)
	}
	_, _ = m.SendAndWait(ctx, "ATE0", 2*time.Second)
	_, _ = m.SendAndWait(ctx, "AT+CMEE=1", 2*time.Second)

	if err := m.ensureSIMReady(ctx, pin); err != nil {
		return err
	}
	if _, err := m.SendAndWait(ctx, "AT+CMGF=0", 2*time.Second); err != nil {
		return fmt.Errorf("CMGF: %w", err)
	}
	if _, err := m.SendAndWait(ctx, "AT+CNMI=2,1,0,0,0", 2*time.Second); err != nil {
		// Some modules quietly reject CNMI variants; the polling fallback in
		// the main loop covers us, so don't fail init.
	}
	if _, err := m.SendAndWait(ctx, `AT+CPMS="SM","SM","SM"`, 2*time.Second); err != nil {
		// Same story — many ML307 builds default to "ME"; not fatal.
	}
	return nil
}

func (m *Modem) ensureSIMReady(ctx context.Context, pin string) error {
	lines, err := m.SendAndWait(ctx, "AT+CPIN?", 5*time.Second)
	if err != nil {
		return fmt.Errorf("CPIN?: %w", err)
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "+CPIN: READY"):
			return nil
		case strings.HasPrefix(l, "+CPIN: SIM PIN"):
			if pin == "" {
				return errors.New("SIM PIN required but SMS_AGENT_SIM_PIN not set")
			}
			if _, err := m.SendAndWait(ctx, fmt.Sprintf(`AT+CPIN="%s"`, pin), 8*time.Second); err != nil {
				return fmt.Errorf("CPIN unlock: %w", err)
			}
			return nil
		}
	}
	return errors.New("CPIN? returned no recognisable status")
}

// SignalDBm queries AT+CSQ and converts the rssi field to dBm. Returns
// (rssi, ok). Kept for older call sites; new code should use SignalMetrics
// to get LTE-aware RSRP/RSRQ where the modem supports CESQ.
//
//	+CSQ: <rssi>,<ber>
//	rssi: 0–31 mapped to -113..-51 dBm in 2 dBm steps; 99 = unknown.
func (m *Modem) SignalDBm(ctx context.Context) (rssi int, ok bool) {
	return m.csqDBm(ctx)
}

// SignalMetrics queries the modem for full signal info. AT+CESQ is preferred
// on LTE modules because it returns RSRP/RSRQ separately; AT+CSQ is the
// universal fallback and returns only a single "rssi" gauge.
//
// Field semantics:
//   - rssi : legacy RSSI in dBm, populated whenever either CESQ or CSQ
//     succeeds. The backend stores this in devices.signal_rssi so the panel
//     has SOMETHING to display on 2G-only / no-LTE-firmware modems.
//   - rsrp / rsrq : LTE-only metrics from CESQ. ptr-set only when CESQ
//     reported a usable value; nil means "modem didn't tell us".
//
// We try CESQ first — its 2 LTE fields are strictly more useful than CSQ's
// single rssi for any operator looking at signal quality. If CESQ ERRORS or
// returns no parseable line, we fall back to CSQ. Older non-LTE modems will
// trip the fallback every heartbeat; that's intentional (the alternative is
// having no signal info at all on a working 2G fleet).
func (m *Modem) SignalMetrics(ctx context.Context) (rssi int, rsrp *int, rsrq *int, ok bool) {
	// AT+CESQ: <rxlev>,<ber>,<rscp>,<ecno>,<rsrq>,<rsrp>
	// rxlev: 0–63 → -110..-48 dBm (1 dBm step); 99 = unknown
	// rsrp:  0–97 → -141..-44 dBm (1 dBm step); 255 = unknown
	// rsrq:  0–34 → -20..-3 dB (0.5 dB step); 255 = unknown
	lines, err := m.SendAndWait(ctx, "AT+CESQ", 2*time.Second)
	if err == nil {
		for _, l := range lines {
			if !strings.HasPrefix(l, "+CESQ:") {
				continue
			}
			val := strings.TrimSpace(strings.TrimPrefix(l, "+CESQ:"))
			parts := strings.Split(val, ",")
			if len(parts) < 6 {
				continue
			}
			gotAny := false
			if v, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil && v != 99 {
				rssi = -110 + v
				gotAny = true
			}
			if v, err := strconv.Atoi(strings.TrimSpace(parts[5])); err == nil && v != 255 {
				dbm := -141 + v
				rsrp = &dbm
				gotAny = true
			}
			if v, err := strconv.Atoi(strings.TrimSpace(parts[4])); err == nil && v != 255 {
				// RSRQ uses 0.5 dB steps starting at -20 dB; round to dB.
				db := -20 + v/2
				rsrq = &db
				gotAny = true
			}
			if gotAny {
				return rssi, rsrp, rsrq, true
			}
		}
	}
	// Fall back to CSQ.
	if v, ok := m.csqDBm(ctx); ok {
		return v, nil, nil, true
	}
	return 0, nil, nil, false
}

func (m *Modem) csqDBm(ctx context.Context) (int, bool) {
	lines, err := m.SendAndWait(ctx, "AT+CSQ", 2*time.Second)
	if err != nil {
		return 0, false
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "+CSQ:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(l, "+CSQ:"))
		parts := strings.SplitN(val, ",", 2)
		rssi, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || rssi == 99 {
			return 0, false
		}
		return -113 + 2*rssi, true
	}
	return 0, false
}

// iccidCacheTTL bounds how long a cached ICCID is reused before re-probing.
// ICCID is immutable per SIM, so the cache is correct as long as the same
// SIM is in the slot. A hot-swap (operator pulls and inserts a different
// SIM mid-run) will be detected on the next probe after this window —
// 10 minutes balances "ICCID disappears fast enough to be useful for ops"
// against "we don't pummel the modem with redundant probes".
const iccidCacheTTL = 10 * time.Minute

// ICCID returns the SIM card serial. Tries vendor-specific commands in a
// fallback chain since no two modems agree on the right one:
//   - AT+CCID   — Quectel, SIMCom, generic 3GPP
//   - AT+QCCID  — older Quectel firmware
//   - AT+ICCID  — ASR / 翱捷 / 合宙 Air 系列（最常见的国产模组答案）
//   - AT^ICCID  — 华为/海思
//
// The result is cached for iccidCacheTTL. On TTL expiry we re-probe; if the
// new value differs from the cached one we log a SIM-swap warning and
// update. If the re-probe fails entirely (modem busy, SIM removed,
// transient error) we keep the cached value rather than emitting "" — the
// next heartbeat retries naturally. Empty string still means "we never had
// one", which the backend treats as optional.
func (m *Modem) ICCID(ctx context.Context) string {
	m.iccidMu.Lock()
	if m.iccidCache != "" && time.Since(m.iccidCachedAt) < iccidCacheTTL {
		v := m.iccidCache
		m.iccidMu.Unlock()
		return v
	}
	prev := m.iccidCache
	m.iccidMu.Unlock()

	fresh := m.probeICCID(ctx)

	m.iccidMu.Lock()
	defer m.iccidMu.Unlock()
	if fresh == "" {
		// Probe failed. Keep the cached value (likely transient) but do NOT
		// extend cachedAt — next heartbeat will retry, so a real SIM removal
		// surfaces quickly via repeated probe failures rather than dragging
		// for a full TTL.
		return m.iccidCache
	}
	if prev != "" && prev != fresh {
		log.Warn().Str("old", prev).Str("new", fresh).Msg("SIM swap detected: ICCID changed")
	}
	m.iccidCache = fresh
	m.iccidCachedAt = time.Now()
	return fresh
}

// InvalidateICCID drops the cached ICCID so the next ICCID call re-probes
// the modem. Call this when a SIM-state signal arrives from another path
// (e.g., +CMS ERROR: 310 "SIM not inserted" on a send attempt, or an
// operator-triggered SIM-swap command).
func (m *Modem) InvalidateICCID() {
	m.iccidMu.Lock()
	m.iccidCache = ""
	m.iccidCachedAt = time.Time{}
	m.iccidMu.Unlock()
}

// IMEI returns the modem equipment identity from AT+CGSN.
func (m *Modem) IMEI(ctx context.Context) string {
	var fallback string
	for _, cmd := range []string{"AT+CGSN=1", "AT+GSN", "AT+QGSN", "AT+CGSN"} {
		lines, err := m.SendAndWait(ctx, cmd, 3*time.Second)
		if err != nil {
			continue
		}
		for _, l := range lines {
			s := modemIdentityFromLine(l)
			if len(s) >= 14 && len(s) <= 17 && isAllDigit(s) {
				return s
			}
			if fallback == "" && len(s) >= 10 && len(s) <= 32 && isAllAlphaNum(s) {
				fallback = s
			}
		}
	}
	return fallback
}

func modemIdentityFromLine(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if s == "" || strings.HasPrefix(strings.ToUpper(s), "AT") {
		return ""
	}
	for _, prefix := range []string{"+CGSN:", "+GSN:", "IMEI:"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
	}
	if comma := strings.IndexByte(s, ','); comma >= 0 {
		s = s[:comma]
	}
	return strings.Trim(strings.TrimSpace(s), `"`)
}

// PhoneNumber queries AT+CNUM (subscriber's own MSISDN). Empty string is
// the common case — most Chinese consumer SIMs leave SDN/MSISDN blank, and
// IoT cards almost never carry it. The panel treats "" as "unknown".
//
//	+CNUM: <alpha>,<number>,<type>[,<speed>,<service>]
//
// Some firmwares emit a bare "+CNUM:" line on empty result; we just keep
// returning "" in that case.
func (m *Modem) PhoneNumber(ctx context.Context) string {
	lines, err := m.SendAndWait(ctx, "AT+CNUM", 3*time.Second)
	if err != nil {
		return ""
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "+CNUM:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(l, "+CNUM:"))
		// CSV with quoted alpha + number; we want field[1].
		parts := splitCSVQuoted(val)
		if len(parts) < 2 {
			continue
		}
		num := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		if num != "" {
			return num
		}
	}
	return ""
}

// Carrier queries AT+COPS? to get the registered operator name. Format:
//
//	+COPS: <mode>,<format>,"<oper>"[,<AcT>]
//
// We force <format>=0 (alphanumeric long) with AT+COPS=3,0 before reading,
// so we get "CHINA MOBILE" / "CMCC" rather than a numeric MCC+MNC. The
// alphanumeric form is what operators expect to see in the panel; we leave
// MCC+MNC translation as a future enhancement if anyone ever cares.
//
// Returns "" if the modem is not registered to any network (e.g., SIM
// removed, no signal) — the panel treats "" as "unknown" and keeps the
// previously-known value via COALESCE in the heartbeat UPDATE.
func (m *Modem) Carrier(ctx context.Context) string {
	_, _ = m.SendAndWait(ctx, "AT+COPS=3,0", 2*time.Second)
	lines, err := m.SendAndWait(ctx, "AT+COPS?", 3*time.Second)
	if err != nil {
		return ""
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "+COPS:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(l, "+COPS:"))
		parts := splitCSVQuoted(val)
		if len(parts) < 3 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(parts[2]), `"`)
		return name
	}
	return ""
}

// splitCSVQuoted splits "a","b,c",d into ["a", "\"b,c\"", "d"]. Quotes are
// preserved so callers can decide whether to strip them. AT response CSV is
// always quoted-with-double-quotes per 3GPP 27.005; no embedded escaping.
func splitCSVQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQ = !inQ
			cur.WriteByte(c)
		case c == ',' && !inQ:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}

func (m *Modem) probeICCID(ctx context.Context) string {
	for _, cmd := range []string{"AT+CCID", "AT+QCCID", "AT+ICCID", "AT^ICCID"} {
		lines, err := m.SendAndWait(ctx, cmd, 2*time.Second)
		if err != nil {
			continue
		}
		for _, l := range lines {
			// Some modules wrap the value in "+CCID: 89..." others return bare.
			s := strings.TrimSpace(l)
			for _, prefix := range []string{"+CCID:", "+QCCID:", "+ICCID:", "^ICCID:"} {
				s = strings.TrimPrefix(s, prefix)
			}
			s = strings.TrimSpace(s)
			// Some modems quote the value: "898600..." → strip the quotes.
			s = strings.Trim(s, `"`)
			if isAllHexDigit(s) && len(s) >= 18 {
				return s
			}
		}
	}
	return ""
}

func isAllHexDigit(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func isAllDigit(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isAllAlphaNum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
	}
	return true
}

// StoredMessage is one entry returned by AT+CMGL=4.
type StoredMessage struct {
	Index int
	PDU   string
}

// ListStored returns all messages currently in SIM storage (read + unread).
// PDU mode AT+CMGL=4 yields header lines like:
//
//	+CMGL: <index>,<stat>,<alpha>,<length>
//	<pdu in hex>
func (m *Modem) ListStored(ctx context.Context) ([]StoredMessage, error) {
	lines, err := m.SendAndWait(ctx, "AT+CMGL=4", 10*time.Second)
	if err != nil {
		return nil, err
	}
	var out []StoredMessage
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "+CMGL:") {
			continue
		}
		hdr := strings.TrimSpace(strings.TrimPrefix(lines[i], "+CMGL:"))
		parts := strings.SplitN(hdr, ",", 2)
		idx, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		if i+1 >= len(lines) {
			break
		}
		out = append(out, StoredMessage{Index: idx, PDU: strings.TrimSpace(lines[i+1])})
		i++ // skip the PDU body line
	}
	return out, nil
}

// DeleteStored removes a single message from SIM storage.
func (m *Modem) DeleteStored(ctx context.Context, index int) error {
	_, err := m.SendAndWait(ctx, fmt.Sprintf("AT+CMGD=%d", index), 5*time.Second)
	return err
}

// Send transmits a body to recipient. We encode locally to a PDU, then issue
// AT+CMGS=<tpduLen>, wait for the ">" prompt, send the hex PDU followed by
// Ctrl-Z, and wait for the final OK.
//
// udh carries optional 8-bit concat metadata; the zero value sends a plain
// single-part SMS (the legacy path). Non-zero udh.Total instructs EncodeSubmit
// to emit a UDHI-flagged PDU with the IEI 0x00 header so the recipient
// handset reassembles multi-segment sends into one balloon.
func (m *Modem) Send(ctx context.Context, recipient, body string, udh SubmitUDH) error {
	hexPDU, tpduLen, err := EncodeSubmit(recipient, body, udh)
	if err != nil {
		return err
	}
	// CMGS prompts with ">" — our reader treats the prompt as a result. We
	// then write hex + ctrl-Z directly, then need a follow-up wait for OK.
	if _, err := m.SendAndWait(ctx, fmt.Sprintf("AT+CMGS=%d", tpduLen), 5*time.Second); err != nil {
		return fmt.Errorf("CMGS init: %w", err)
	}
	// Queue a no-op call so readerLoop knows the next OK/ERROR belongs to us.
	c := &call{cmd: "", done: make(chan callResult, 1), timeout: 30 * time.Second}
	m.mu.Lock()
	if err := m.SendRaw([]byte(hexPDU)); err != nil {
		m.mu.Unlock()
		return err
	}
	if err := m.SendRaw([]byte{0x1A}); err != nil {
		m.mu.Unlock()
		return err
	}
	m.pending <- c
	m.mu.Unlock()

	select {
	case res := <-c.done:
		return res.err
	case <-time.After(30 * time.Second):
		return errors.New("CMGS body: timeout waiting for OK")
	case <-ctx.Done():
		return ctx.Err()
	case <-m.closed:
		return errors.New("modem closed")
	}
}
