// Package modem drives a 4G AT-command modem over a serial port.
//
// Target: ML307R-DC / ML307A-DC. The AT command set for basic SMS
// operations (CMGF, CMGL, CMGS, CSQ) is shared across ML307 variants.
//
// Concurrency model: a single goroutine inside readerLoop owns the serial
// port reader. Public methods Send/SendAndWait push commands onto a channel
// and receive the response payload. The reader strips echo and aggregates
// unsolicited result codes (URCs) like "+CMTI:" through a registered
// notification handler so the caller can react to new-SMS notifications
// without polling.
package modem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.bug.st/serial"

	"dji-modem-research/third_party/smscodec"
)

type Modem struct {
	// port is the transport carrying AT bytes. In production this is a USB
	// bulk endpoint (see internal/usbtransport); over serial it is a
	// go.bug.st/serial.Port (which satisfies io.ReadWriteCloser). Read must
	// have a short-timeout poll semantic (e.g. 200ms) so readerLoop can wake
	// up to observe m.closed — see NewFromIO / Open.
	port io.ReadWriteCloser

	mu        sync.Mutex
	pending   chan *call // currently in-flight call, nil if idle
	closeOnce sync.Once
	closed    chan struct{}

	urcMu       sync.Mutex
	urcHandlers []URCHandler

	iccidMu       sync.Mutex
	iccidCache    string
	iccidCachedAt time.Time

	// Reassembler accumulates incoming long-SMS segments until complete. Fed
	// by the +CMTI auto-read handler (see sms.go handleIncomingSMS).
	reassembler *smscodec.Reassembler

	// smsCallback is invoked when a complete SMS (single-part or reassembled)
	// arrives via the +CMTI auto-read pipeline. nil = auto-read disabled.
	smsCbMu sync.Mutex
	smsCb   SMSCallback
}

// SMSCallback is invoked when a complete SMS arrives via the +CMTI auto-read
// pipeline (see SetSMSCallback). sender is the originator MSISDN, content is
// the (possibly reassembled) message body, ts is the service-center timestamp.
type SMSCallback func(sender, content string, ts time.Time)

type URCHandler func(line string)

type call struct {
	cmd     string
	prefix  string // optional response-line prefix to capture (e.g. "+CMGL:")
	done    chan callResult
	timeout time.Duration
}

type callResult struct {
	lines []string
	err   error
}

// Open a serial port at the given baud rate.
func Open(port string, baud int) (*Modem, error) {
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	p, err := serial.Open(port, mode)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", port, err)
	}
	if err := p.SetReadTimeout(200 * time.Millisecond); err != nil {
		_ = p.Close()
		return nil, err
	}
	m := &Modem{
		port:        p,
		pending:     make(chan *call, 1),
		closed:      make(chan struct{}),
		reassembler: smscodec.NewReassembler(),
	}
	go m.readerLoop()
	return m, nil
}

// NewFromIO wraps any io.ReadWriteCloser (e.g. a USB bulk endpoint) as a
// Modem, bypassing the serial-port Open path. The caller is responsible for
// ensuring Read uses a short-timeout poll (on the order of 200ms) so that
// readerLoop can wake up periodically and observe Close — a permanently
// blocking Read would prevent shutdown.
func NewFromIO(port io.ReadWriteCloser) *Modem {
	m := &Modem{
		port:        port,
		pending:     make(chan *call, 1),
		closed:      make(chan struct{}),
		reassembler: smscodec.NewReassembler(),
	}
	go m.readerLoop()
	return m
}
func (m *Modem) Close() error {
	var err error
	m.closeOnce.Do(func() {
		close(m.closed)
		err = m.port.Close()
	})
	return err
}

// OnURC registers a handler for unsolicited result codes — lines the modem
// emits asynchronously (e.g. "+CMTI: \"SM\",3"). Handlers are invoked from
// the reader goroutine; they must not block.
func (m *Modem) OnURC(h URCHandler) {
	m.urcMu.Lock()
	m.urcHandlers = append(m.urcHandlers, h)
	m.urcMu.Unlock()
}

func (m *Modem) dispatchURC(line string) {
	m.urcMu.Lock()
	hs := append([]URCHandler(nil), m.urcHandlers...)
	m.urcMu.Unlock()
	for _, h := range hs {
		h(line)
	}
}

// SendAndWait writes cmd, then reads lines until OK / ERROR / +CMS ERROR /
// +CME ERROR / timeout. Returns the response lines (excluding the terminator).
func (m *Modem) SendAndWait(ctx context.Context, cmd string, timeout time.Duration) ([]string, error) {
	c := &call{cmd: cmd, done: make(chan callResult, 1), timeout: timeout}
	m.mu.Lock()
	// AT trace — only stringifies under debug, so the hot path stays cheap at
	// the default info level. Run before Write so we still see the cmd even if
	// the port write itself fails.
	log.Debug().Str("cmd", cmd).Dur("timeout", timeout).Msg("AT →")
	if _, err := m.port.Write([]byte(cmd + "\r\n")); err != nil {
		m.mu.Unlock()
		log.Debug().Err(err).Str("cmd", cmd).Msg("AT × write failed")
		return nil, err
	}
	m.pending <- c
	m.mu.Unlock()

	select {
	case res := <-c.done:
		ev := log.Debug().Str("cmd", cmd).Strs("lines", res.lines)
		if res.err != nil {
			ev.Err(res.err).Msg("AT ← error")
		} else {
			ev.Msg("AT ← ok")
		}
		return res.lines, res.err
	case <-time.After(timeout):
		// Drain the pending slot so a slow URC doesn't deadlock the next call.
		select {
		case <-m.pending:
		default:
		}
		log.Debug().Str("cmd", cmd).Dur("timeout", timeout).Msg("AT ← timeout")
		return nil, fmt.Errorf("AT timeout: %q", cmd)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.closed:
		return nil, io.ErrClosedPipe
	}
}

// SendRaw writes a raw payload without the \r\n terminator. Used for the SMS
// body part of CMGS, which terminates with Ctrl-Z (0x1A) instead.
func (m *Modem) SendRaw(p []byte) error {
	_, err := m.port.Write(p)
	return err
}

// PingResult mirrors firmware/modem.h's struct so the ack summary string the
// operator sees on Linux is the same as on ESP32 ("ip=8.8.8.8 rtt=275ms ttl=107").
type PingResult struct {
	OK         bool
	ResultCode int    // raw value from the native ping URC
	RTTMs      int    // -1 if unknown
	TTL        int    // -1 if unknown
	TargetIP   string // echoed back from the URC
	Err        string // human-readable failure summary
}

type pingDialect struct {
	name      string
	urcPrefix string
	buildCmd  func(target string, timeoutS, count int) string
	parse     func(line string, res *PingResult)
}

var (
	pingDialectMPING = pingDialect{
		name:      "MPING",
		urcPrefix: "+MPING:",
		buildCmd: func(target string, timeoutS, count int) string {
			return fmt.Sprintf(`AT+MPING="%s",%d,%d`, target, timeoutS, count)
		},
		parse: parseMPING,
	}
	pingDialectQPING = pingDialect{
		name:      "QPING",
		urcPrefix: "+QPING:",
		buildCmd: func(target string, timeoutS, count int) string {
			return fmt.Sprintf(`AT+QPING=1,"%s",%d,%d`, target, timeoutS, count)
		},
		parse: parseQPING,
	}
	pingDialectCIPPING = pingDialect{
		name:      "CIPPING",
		urcPrefix: "+CIPPING:",
		buildCmd: func(target string, timeoutS, count int) string {
			return fmt.Sprintf(`AT+CIPPING="%s",%d,32,%d,64`, target, count, timeoutS)
		},
		parse: parseCIPPING,
	}
)

// IcmpPing runs a module-native cellular keepalive ping. The flow mirrors the
// ESP32 firmware: bring PDP up, fire the modem's ICMP command, wait for its
// URC, then tear PDP back down. The URC handling is wired through OnURC so
// concurrent SMS receive (+CMTI) and other URCs continue to dispatch normally.
//
// target must be an IPv4 literal. timeoutS / count are clamped to the same
// 1-60 / 1-8 bounds the firmware uses.
func (m *Modem) IcmpPing(ctx context.Context, target string, timeoutS, count int) PingResult {
	res := PingResult{ResultCode: -1, RTTMs: -1, TTL: -1}
	if target == "" {
		res.Err = "empty target"
		return res
	}
	if timeoutS < 1 {
		timeoutS = 1
	} else if timeoutS > 60 {
		timeoutS = 60
	}
	if count < 1 {
		count = 1
	} else if count > 8 {
		count = 8
	}

	// Best-effort PDP activate. We deliberately don't error out the ping just
	// because CGACT returned non-OK — on many firmwares the PDP is already up
	// after boot and CGACT=1,1 returns "+CME ERROR: 100" (already activated).
	// The native ping itself will fail cleanly if the data plane is truly down,
	// and that's the result the operator wants to see.
	if _, err := m.SendAndWait(ctx, "AT+CGACT=1,1", 10*time.Second); err != nil {
		log.Debug().Err(err).Msg("CGACT=1,1 returned non-OK, attempting native ping anyway")
	}
	// Small settle delay; CGACT can return OK before the data plane is
	// actually serviceable. Matches the field-tested firmware delay.
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		res.Err = ctx.Err().Error()
		return res
	}

	var lastSetupErr error
	for _, dialect := range m.pingDialects(ctx) {
		urcCh := make(chan string, 4)
		removeHandler := m.capturePingURC(dialect.urcPrefix, urcCh)
		cmd := dialect.buildCmd(target, timeoutS, count)
		log.Debug().Str("dialect", dialect.name).Str("cmd", cmd).Msg("attempting ICMP ping")

		lines, err := m.SendAndWait(ctx, cmd, 5*time.Second)
		if line, ok := firstLineWithPrefix(lines, dialect.urcPrefix); ok {
			dialect.parse(line, &res)
			removeHandler()
			break
		}
		if err != nil {
			removeHandler()
			lastSetupErr = err
			log.Debug().Err(err).Str("dialect", dialect.name).Msg("ping command rejected, trying next dialect")
			continue
		}

		// The setup command normally returns OK quickly; the actual result
		// arrives as a URC up to timeoutS seconds later.
		timer := time.NewTimer(time.Duration(timeoutS+5) * time.Second)
		select {
		case line := <-urcCh:
			dialect.parse(line, &res)
		case <-timer.C:
			res.Err = fmt.Sprintf("timeout waiting for %s URC", dialect.urcPrefix)
		case <-ctx.Done():
			res.Err = ctx.Err().Error()
		}
		timer.Stop()
		removeHandler()
		break
	}

	if res.ResultCode < 0 && res.Err == "" && lastSetupErr != nil {
		res.Err = "ping setup failed: " + lastSetupErr.Error()
	}

	// Tear PDP down best-effort. The ping outcome is already decided.
	_, _ = m.SendAndWait(ctx, "AT+CGACT=0,1", 5*time.Second)

	if !res.OK && res.Err == "" {
		if res.ResultCode < 0 {
			res.Err = "ping failed (no result code)"
		} else {
			res.Err = fmt.Sprintf("ping failed (code=%d)", res.ResultCode)
		}
	}
	return res
}

func (m *Modem) capturePingURC(prefix string, ch chan<- string) func() {
	m.urcMu.Lock()
	idx := len(m.urcHandlers)
	m.urcHandlers = append(m.urcHandlers, func(line string) {
		if strings.HasPrefix(line, prefix) {
			select {
			case ch <- line:
			default:
			}
		}
	})
	m.urcMu.Unlock()
	return func() {
		m.urcMu.Lock()
		if idx < len(m.urcHandlers) {
			m.urcHandlers = append(m.urcHandlers[:idx], m.urcHandlers[idx+1:]...)
		}
		m.urcMu.Unlock()
	}
}

func (m *Modem) pingDialects(ctx context.Context) []pingDialect {
	identity := strings.ToLower(strings.Join(m.pingIdentity(ctx), " "))
	switch {
	case strings.Contains(identity, "quectel") ||
		strings.Contains(identity, "baiwang") ||
		strings.Contains(identity, "qdc507") ||
		strings.Contains(identity, "qdc507gle") ||
		strings.Contains(identity, "ec20") ||
		strings.Contains(identity, "ec25") ||
		strings.Contains(identity, "ec200") ||
		strings.Contains(identity, "ec600") ||
		strings.Contains(identity, "eg25") ||
		strings.Contains(identity, "bg95"):
		return []pingDialect{pingDialectQPING, pingDialectMPING, pingDialectCIPPING}
	case strings.Contains(identity, "simcom") ||
		strings.Contains(identity, "sim7600") ||
		strings.Contains(identity, "sim7000") ||
		strings.Contains(identity, "a76"):
		return []pingDialect{pingDialectCIPPING, pingDialectQPING, pingDialectMPING}
	case strings.Contains(identity, "ml307") ||
		strings.Contains(identity, "mobiletek") ||
		strings.Contains(identity, "airm2m") ||
		strings.Contains(identity, "luat"):
		return []pingDialect{pingDialectMPING, pingDialectQPING, pingDialectCIPPING}
	default:
		return []pingDialect{pingDialectMPING, pingDialectQPING, pingDialectCIPPING}
	}
}

func (m *Modem) pingIdentity(ctx context.Context) []string {
	var out []string
	for _, cmd := range []string{"AT+CGMI", "AT+CGMM", "ATI"} {
		lines, err := m.SendAndWait(ctx, cmd, 1200*time.Millisecond)
		if err != nil {
			continue
		}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.EqualFold(line, cmd) {
				continue
			}
			out = append(out, line)
		}
	}
	return out
}

func firstLineWithPrefix(lines []string, prefix string) (string, bool) {
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

// parseMPING parses one URC line like:
//
//	+MPING: <result>[,<ip>,<len>,<time>,<ttl>]
//
// IP can be quoted or bare. Mirrors firmware/modem.cpp's parser; the field
// shape is stable across the ML307 firmware revisions we've seen.
func parseMPING(line string, res *PingResult) {
	fields := pingFields(line)
	if len(fields) == 0 {
		return
	}
	code, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		return
	}
	res.ResultCode = code
	res.OK = (code == 0 || code == 1)
	if len(fields) >= 2 {
		res.TargetIP = cleanATField(fields[1])
	}
	// fields[2] = packet length — skip
	if len(fields) >= 4 {
		if t, err := strconv.Atoi(strings.TrimSpace(fields[3])); err == nil {
			res.RTTMs = t
		}
	}
	if len(fields) >= 5 {
		if t, err := strconv.Atoi(strings.TrimSpace(fields[4])); err == nil {
			res.TTL = t
		}
	}
}

// parseQPING handles Quectel's QPING forms:
//
//	+QPING: 0,"8.8.8.8",32,58,255
//	+QPING: 0,1,1,0,58,58,58
func parseQPING(line string, res *PingResult) {
	fields := pingFields(line)
	if len(fields) == 0 {
		return
	}
	code, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		return
	}
	res.ResultCode = code
	res.OK = code == 0
	if len(fields) >= 2 && looksLikeIP(cleanATField(fields[1])) {
		res.TargetIP = cleanATField(fields[1])
		if len(fields) >= 4 {
			res.RTTMs = atoiDefault(fields[3], res.RTTMs)
		}
		if len(fields) >= 5 {
			res.TTL = atoiDefault(fields[4], res.TTL)
		}
		return
	}
	if len(fields) >= 3 {
		received := atoiDefault(fields[2], 0)
		res.OK = code == 0 && received > 0
	}
	if len(fields) >= 7 {
		res.RTTMs = atoiDefault(fields[6], res.RTTMs)
	} else if len(fields) >= 5 {
		res.RTTMs = atoiDefault(fields[4], res.RTTMs)
	}
}

// parseCIPPING handles the common SIMCom per-packet form:
//
//	+CIPPING: 1,"8.8.8.8",32,58,255
func parseCIPPING(line string, res *PingResult) {
	fields := pingFields(line)
	if len(fields) == 0 {
		return
	}
	res.ResultCode = 0
	if len(fields) >= 2 && looksLikeIP(cleanATField(fields[1])) {
		res.OK = true
		res.TargetIP = cleanATField(fields[1])
		if len(fields) >= 4 {
			res.RTTMs = atoiDefault(fields[3], res.RTTMs)
		}
		if len(fields) >= 5 {
			res.TTL = atoiDefault(fields[4], res.TTL)
		}
		return
	}
	code, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil {
		return
	}
	res.ResultCode = code
	res.OK = code == 0
	if len(fields) >= 3 {
		res.OK = code == 0 && atoiDefault(fields[2], 0) > 0
	}
}

func pingFields(line string) []string {
	colon := strings.Index(line, ":")
	if colon < 0 {
		return nil
	}
	return splitATCSV(strings.TrimSpace(line[colon+1:]))
}

func splitATCSV(s string) []string {
	var fields []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch r {
		case '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case ',':
			if inQuote {
				b.WriteRune(r)
				continue
			}
			fields = append(fields, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	fields = append(fields, strings.TrimSpace(b.String()))
	return fields
}

func cleanATField(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"`)
}

func looksLikeIP(s string) bool {
	return strings.Count(s, ".") == 3
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(cleanATField(s)))
	if err != nil {
		return def
	}
	return n
}

// readerLoop is the only goroutine that reads from the port. It dispatches
// lines either to the in-flight call or to URC handlers.
func (m *Modem) readerLoop() {
	r := bufio.NewReader(m.port)
	var current *call
	var collected []string

	for {
		select {
		case <-m.closed:
			return
		default:
		}

		line, err := readLine(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				continue
			}
			// Read timeouts manifest as short reads on go.bug.st/serial; treat
			// them as nothing-to-do and try again. Other errors we surface.
			if isTimeout(err) {
				// Latch in-flight call from the pending slot if we don't have one.
				if current == nil {
					select {
					case c := <-m.pending:
						current = c
					default:
					}
				}
				continue
			}
			// bufio surfaces a sustained run of zero-byte serial reads as
			// ErrNoProgress. The serial layer is healthy; nothing arrived in
			// the window. Drop it silently so idle logs stay quiet.
			if errors.Is(err, io.ErrNoProgress) {
				continue
			}
			log.Debug().Err(err).Msg("modem read error")
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if current == nil {
			select {
			case c := <-m.pending:
				current = c
				collected = nil
			default:
			}
		}

		// Echo of our own command — discard.
		if current != nil && line == current.cmd {
			continue
		}

		// Terminators.
		if isTerminator(line) {
			if current != nil {
				res := callResult{lines: collected}
				if strings.HasPrefix(line, "ERROR") || strings.HasPrefix(line, "+CMS ERROR") || strings.HasPrefix(line, "+CME ERROR") {
					res.err = fmt.Errorf("modem: %s", line)
				}
				current.done <- res
				current = nil
				collected = nil
			}
			continue
		}

		// Prompt for CMGS body input — surface as a single-line result and
		// keep the call open until the next OK arrives (the caller writes the
		// PDU + Ctrl-Z and then re-invokes a wait via a follow-up call).
		if line == ">" {
			if current != nil {
				current.done <- callResult{lines: append(collected, ">")}
				current = nil
				collected = nil
			}
			continue
		}

		if current != nil {
			collected = append(collected, line)
		} else {
			log.Debug().Str("urc", line).Msg("AT ⇢ URC")
			m.dispatchURC(line)
		}
	}
}

func isTerminator(line string) bool {
	switch {
	case line == "OK":
		return true
	case line == "ERROR":
		return true
	case strings.HasPrefix(line, "+CMS ERROR"):
		return true
	case strings.HasPrefix(line, "+CME ERROR"):
		return true
	}
	return false
}

func isTimeout(err error) bool {
	// go.bug.st/serial returns nil on a timeout (zero bytes read). bufio
	// surfaces this as io.EOF — so the readerLoop loops back. Defensive in
	// case behaviour changes across platforms.
	type timeout interface{ Timeout() bool }
	if t, ok := err.(timeout); ok && t.Timeout() {
		return true
	}
	return false
}

// readLine reads one logical line from r.
//
// It returns a complete line once either:
//   - a '\n'-terminated line is available (the normal AT response case), or
//   - the buffered content (no '\n' yet) is exactly the CMGS ">" prompt.
//
// The ">" special case is necessary because the CMGS prompt is terminated by a
// space, not '\n'. This implementation reads one byte at a time so it can
// detect ">" immediately after it arrives, without depending on the transport
// returning a timeout error. This makes it work both with short-timeout
// polling (serial) and the long-lived blocking read (USB, direction F —
// issue/001), where a Read blocks until data arrives and ReadString('\n')
// would deadlock waiting for a '\n' that the ">" prompt never sends.
func readLine(r *bufio.Reader) (string, error) {
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			buf = append(buf, one[:n]...)
			// Complete line?
			if one[0] == '\n' {
				return string(buf), nil
			}
			// CMGS prompt? ">" arrives without a trailing '\n'. Detect it as
			// soon as the trimmed buffer equals ">".
			if strings.TrimSpace(string(buf)) == ">" {
				return string(buf), nil
			}
		}
		if err != nil {
			return string(buf), err
		}
	}
}
