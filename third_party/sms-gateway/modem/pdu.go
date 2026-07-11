// Package modem — PDU encoding/decoding for SMS-DELIVER (RX) and SMS-SUBMIT
// (TX). Hand-rolled subset of 3GPP TS 23.040 + TS 23.038:
//
//   - 7-bit GSM default alphabet, packed and unpacked. No GSM-7 extension
//     table — characters outside the basic set get the substitute '?'.
//   - UCS2 (UTF-16BE) for messages containing characters outside the basic
//     GSM-7 set. Auto-selected by EncodeSubmit when the body has any non-7-bit
//     character.
//   - Receive-side UDH/concat metadata extraction. Transmit-side multi-part
//     encoding is not supported; messages longer than one PDU are rejected by
//     EncodeSubmit and the caller can split them.
//
// Scope is intentionally narrow: enough to talk to a typical SIM7600 / ML307R
// in the field, not a complete TPDU library. The receive path is forgiving —
// we extract sender/body/timestamp and ignore anything we don't understand
// (PID, status reports, etc.).
package modem

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

type DecodedSMS struct {
	Sender    string
	Body      string
	Timestamp time.Time
	RawPDU    string // upper-case hex of the original PDU
	Concat    *ConcatInfo
}

type ConcatInfo struct {
	Ref   int
	Part  int
	Total int
}

// DecodeDeliver parses a hex-encoded SMS-DELIVER as returned by AT+CMGL=4.
func DecodeDeliver(hexPDU string) (*DecodedSMS, error) {
	hexPDU = strings.TrimSpace(strings.ToUpper(hexPDU))
	raw, err := hex.DecodeString(hexPDU)
	if err != nil {
		return nil, fmt.Errorf("pdu hex: %w", err)
	}
	if len(raw) < 1 {
		return nil, errors.New("pdu too short")
	}
	p := 0

	// SCA: 1-byte length, then 'length' bytes (type + digits).
	scaLen := int(raw[p])
	p++
	p += scaLen
	if p >= len(raw) {
		return nil, errors.New("pdu truncated after SCA")
	}

	// PDU type byte. For SMS-DELIVER MTI bits are 00. UDHI (bit 6) means
	// the user data begins with a User Data Header; concatenated SMS uses it
	// to carry ref/part/total and the header must not be decoded as text.
	pduType := raw[p]
	hasUDH := pduType&0x40 != 0
	p++
	if p >= len(raw) {
		return nil, errors.New("pdu truncated after type")
	}

	// Originator address: <addrLen in semi-octets><type><digits packed>.
	addrLen := int(raw[p])
	p++
	if p >= len(raw) {
		return nil, errors.New("pdu truncated at OA")
	}
	addrType := raw[p]
	p++
	addrDigitsBytes := (addrLen + 1) / 2
	if p+addrDigitsBytes > len(raw) {
		return nil, errors.New("pdu truncated in OA digits")
	}
	sender := decodeAddress(raw[p:p+addrDigitsBytes], addrLen, addrType)
	p += addrDigitsBytes

	// PID + DCS.
	if p+2 > len(raw) {
		return nil, errors.New("pdu truncated at PID/DCS")
	}
	p++ // PID
	dcs := raw[p]
	p++

	// Service Centre Time Stamp — 7 bytes, BCD with swapped nibbles.
	if p+7 > len(raw) {
		return nil, errors.New("pdu truncated at SCTS")
	}
	ts := decodeSCTS(raw[p : p+7])
	p += 7

	// User Data Length and User Data.
	if p >= len(raw) {
		return nil, errors.New("pdu truncated at UDL")
	}
	udl := int(raw[p])
	p++

	body, concat, err := decodeUserData(raw[p:], udl, dcs, hasUDH)
	if err != nil {
		return nil, err
	}
	return &DecodedSMS{
		Sender:    sender,
		Body:      body,
		Timestamp: ts,
		RawPDU:    hexPDU,
		Concat:    concat,
	}, nil
}

// SubmitUDH carries the 8-bit concat header that EncodeSubmit prepends to the
// user data when Total > 1. All three fields must be non-zero and in range
// (1 ≤ Part ≤ Total ≤ 255, 1 ≤ Ref ≤ 255). A zero-value struct (the default)
// means "single-part SMS, emit no UDH" — the legacy path.
type SubmitUDH struct {
	Ref   int
	Part  int
	Total int
}

// EncodeSubmit builds an SMS-SUBMIT PDU. Returns the hex PDU and the TPDU
// length (number of octets *after* the SCA byte) which is what AT+CMGS=
// expects.
//
// When udh.Total > 1 the User Data is prefixed with a 6-octet User Data
// Header (UDHL=05, IEI=00 ref/total/part, IEDL=03) and the first PDU octet
// gets its UDHI bit set. For GSM-7 multipart we pad with fill bits so the
// text septets land on a 7-bit boundary after the UDH; UDL is in septets and
// counts the UDH+fill+text. For UCS-2 multipart UDL is the byte count of
// UDH+text.
func EncodeSubmit(recipient, body string, udh SubmitUDH) (hexPDU string, tpduLen int, err error) {
	addrBytes, addrLen, err := encodeAddress(recipient)
	if err != nil {
		return "", 0, err
	}
	useUCS2 := needsUCS2(body)
	hasUDH := udh.Total > 1
	if hasUDH {
		if udh.Ref < 1 || udh.Ref > 255 ||
			udh.Total < 2 || udh.Total > 255 ||
			udh.Part < 1 || udh.Part > udh.Total {
			return "", 0, fmt.Errorf("invalid UDH: ref=%d part=%d total=%d", udh.Ref, udh.Part, udh.Total)
		}
	}

	// Build the UDH bytes once; same shape for GSM-7 and UCS-2 (the difference
	// is whether we pad to a septet boundary afterwards).
	var udhBytes []byte
	if hasUDH {
		udhBytes = []byte{0x05, 0x00, 0x03, byte(udh.Ref), byte(udh.Total), byte(udh.Part)}
	}

	var ud []byte
	var udl int
	if useUCS2 {
		text := encodeUCS2(body)
		if hasUDH {
			// 134-byte cap: 140 total − 6 UDH = 134 bytes for text per part.
			if len(text) > 134 {
				return "", 0, errors.New("ucs2 multipart segment exceeds 134 octets")
			}
			ud = append(udhBytes, text...)
			udl = len(ud)
		} else {
			if len(text) > 140 {
				return "", 0, errors.New("ucs2 message exceeds 140 octets — multi-part not supported")
			}
			ud = text
			udl = len(ud)
		}
	} else {
		if hasUDH {
			// 6-octet UDH = 48 bits. To align text to a septet boundary we
			// insert (7 - 48%7) % 7 = 1 fill bit at the start of the text.
			// UDL counts in septets: 7 (UDH+fill) + text septets. Per-part
			// text budget is 153 septets (160 single-PDU − 7 UDH-equivalent
			// septets).
			const fillBits = 1
			packedText, septetCount := packGSM7Shifted(body, fillBits)
			if septetCount > 153 {
				return "", 0, errors.New("gsm-7 multipart segment exceeds 153 septets")
			}
			ud = append(udhBytes, packedText...)
			udl = 7 + septetCount
		} else {
			packed, septets := encodeGSM7(body)
			if septets > 160 {
				return "", 0, errors.New("gsm-7 message exceeds 160 septets — multi-part not supported")
			}
			ud = packed
			udl = septets
		}
	}

	// SCA = 00 (use SMSC stored in SIM).
	// PDU type: 0x11 = SMS-SUBMIT with VPF=relative; OR 0x40 (UDHI) when UDH present.
	// MR (message reference) = 0x00 — module overrides.
	// DA = destination address (addrLen + type + digits).
	// PID = 0x00, DCS = 0x00 (GSM7) or 0x08 (UCS2), VP = 0xAA (= 4 days).
	// UDL + UD.
	firstOctet := byte(0x11)
	if hasUDH {
		firstOctet |= 0x40
	}
	var tpdu []byte
	tpdu = append(tpdu, firstOctet)
	tpdu = append(tpdu, 0x00) // MR
	tpdu = append(tpdu, byte(addrLen))
	tpdu = append(tpdu, addrBytes...) // includes type byte
	tpdu = append(tpdu, 0x00)         // PID
	if useUCS2 {
		tpdu = append(tpdu, 0x08)
	} else {
		tpdu = append(tpdu, 0x00)
	}
	tpdu = append(tpdu, 0xAA) // VP — 4 days
	tpdu = append(tpdu, byte(udl))
	tpdu = append(tpdu, ud...)

	full := append([]byte{0x00}, tpdu...) // SCA length 0
	return strings.ToUpper(hex.EncodeToString(full)), len(tpdu), nil
}

// packGSM7Shifted packs body into 7-bit septets, but skips the first
// `fillBits` bits of the output (filling them with zeros). Used when the
// user data starts with a UDH whose octet boundary doesn't align with
// septets: the first 1..6 bits of the first text septet are "fill bits" so
// septet boundaries land cleanly after the UDH.
//
// The returned slice contains only the text bytes (the UDH itself is the
// caller's responsibility to prepend). For UDH IEI 0x00 (6 bytes) fillBits=1
// pushes septet[0] bit 0 to bit position 1 of the first text byte — exactly
// what the spec requires (48 + 1 = 49 ≡ 0 mod 7).
func packGSM7Shifted(body string, fillBits uint) ([]byte, int) {
	septets := make([]byte, 0, len(body))
	for _, r := range body {
		if c, ok := gsm7Index[r]; ok {
			septets = append(septets, c)
		} else {
			septets = append(septets, gsm7Index['?'])
		}
	}
	septetCount := len(septets)
	bitBuf := uint32(0)
	bitN := fillBits // pre-loaded zero fill bits
	var packed []byte
	for _, s := range septets {
		bitBuf |= uint32(s&0x7F) << bitN
		bitN += 7
		for bitN >= 8 {
			packed = append(packed, byte(bitBuf&0xFF))
			bitBuf >>= 8
			bitN -= 8
		}
	}
	if bitN > 0 {
		packed = append(packed, byte(bitBuf&0xFF))
	}
	return packed, septetCount
}

// ── address codec ───────────────────────────────────────────────────────────

func decodeAddress(b []byte, semiOctets int, addrType byte) string {
	digits := make([]byte, 0, semiOctets)
	for i := 0; i < len(b); i++ {
		lo := b[i] & 0x0F
		hi := (b[i] >> 4) & 0x0F
		digits = append(digits, lo)
		if len(digits) < semiOctets {
			digits = append(digits, hi)
		}
	}
	var sb strings.Builder
	// 0x91 = international (+), 0x81 = national. We don't decode 7-bit
	// alphanumeric senders (type 0xD0) — those come through as a hex echo so
	// the operator can still see the sender id in raw_pdu.
	if addrType == 0x91 {
		sb.WriteByte('+')
	}
	for _, d := range digits {
		if d < 10 {
			sb.WriteByte('0' + d)
		}
	}
	return sb.String()
}

func encodeAddress(recipient string) (out []byte, semiOctets int, err error) {
	intl := false
	digits := []byte{}
	for _, r := range recipient {
		switch {
		case r == '+':
			intl = true
		case r >= '0' && r <= '9':
			digits = append(digits, byte(r-'0'))
		case r == ' ' || r == '-':
			// strip
		default:
			return nil, 0, fmt.Errorf("invalid recipient char %q", r)
		}
	}
	if len(digits) == 0 {
		return nil, 0, errors.New("empty recipient")
	}
	typ := byte(0x81) // national
	if intl {
		typ = 0x91
	}
	// Pack semi-octets, low nibble first.
	packed := make([]byte, 0, (len(digits)+1)/2)
	for i := 0; i < len(digits); i += 2 {
		lo := digits[i]
		hi := byte(0x0F)
		if i+1 < len(digits) {
			hi = digits[i+1]
		}
		packed = append(packed, (hi<<4)|lo)
	}
	return append([]byte{typ}, packed...), len(digits), nil
}

// ── timestamp codec ─────────────────────────────────────────────────────────

func decodeSCTS(b []byte) time.Time {
	if len(b) < 7 {
		return time.Time{}
	}
	dec := func(x byte) int {
		// nibble swap, then BCD.
		return int((x>>4)&0x0F) + int(x&0x0F)*10
	}
	year := 2000 + dec(b[0])
	month := dec(b[1])
	day := dec(b[2])
	hour := dec(b[3])
	min := dec(b[4])
	sec := dec(b[5])
	// b[6] is timezone in quarter-hours; high bit of low nibble is sign.
	tzRaw := b[6]
	negative := tzRaw&0x08 != 0
	tzVal := dec(tzRaw &^ 0x08)
	offset := tzVal * 15 * 60
	if negative {
		offset = -offset
	}
	loc := time.FixedZone("SMSC", offset)
	if month < 1 || month > 12 {
		month = 1
	}
	if day < 1 || day > 31 {
		day = 1
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, loc)
}

// ── 7-bit GSM (TS 23.038) ───────────────────────────────────────────────────

// gsm7Set is the GSM default alphabet — character at index i has septet
// value i. We only encode the basic set; characters outside are mapped to '?'.
const gsm7Set = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ\x1bÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

var gsm7Index = func() map[rune]byte {
	m := make(map[rune]byte, len(gsm7Set))
	i := 0
	for _, r := range gsm7Set {
		m[r] = byte(i)
		i++
	}
	return m
}()

func needsUCS2(body string) bool {
	for _, r := range body {
		if _, ok := gsm7Index[r]; !ok {
			return true
		}
	}
	return false
}

func encodeGSM7(body string) (packed []byte, septetCount int) {
	septets := make([]byte, 0, len(body))
	for _, r := range body {
		if c, ok := gsm7Index[r]; ok {
			septets = append(septets, c)
		} else {
			septets = append(septets, gsm7Index['?'])
		}
	}
	septetCount = len(septets)
	// Pack 7-bit septets into 8-bit octets, LSB-first.
	bitBuf := uint32(0)
	bitN := uint(0)
	for _, s := range septets {
		bitBuf |= uint32(s&0x7F) << bitN
		bitN += 7
		for bitN >= 8 {
			packed = append(packed, byte(bitBuf&0xFF))
			bitBuf >>= 8
			bitN -= 8
		}
	}
	if bitN > 0 {
		packed = append(packed, byte(bitBuf&0xFF))
	}
	return
}

func decodeGSM7(packed []byte, septetCount int) string {
	return decodeGSM7FromBitOffset(packed, septetCount, 0)
}

func decodeGSM7FromBitOffset(packed []byte, septetCount int, bitOffset int) string {
	bitBuf := uint32(0)
	bitN := uint(0)
	out := make([]rune, 0, septetCount)
	idx := 0
	byteOffset := bitOffset / 8
	shift := uint(bitOffset % 8)
	if byteOffset >= len(packed) {
		return ""
	}
	for _, b := range packed[byteOffset:] {
		bitBuf |= uint32(b) << bitN
		bitN += 8
		if shift > 0 {
			bitBuf >>= shift
			if bitN >= shift {
				bitN -= shift
			} else {
				bitN = 0
			}
			shift = 0
		}
		for bitN >= 7 && idx < septetCount {
			s := byte(bitBuf & 0x7F)
			bitBuf >>= 7
			bitN -= 7
			idx++
			if int(s) < len(gsm7Runes) {
				out = append(out, gsm7Runes[s])
			} else {
				out = append(out, '?')
			}
		}
	}
	return string(out)
}

var gsm7Runes = []rune(gsm7Set)

// ── UCS2 ────────────────────────────────────────────────────────────────────

func encodeUCS2(body string) []byte {
	units := utf16.Encode([]rune(body))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u>>8), byte(u&0xFF))
	}
	return out
}

func decodeUCS2(b []byte) string {
	n := len(b) / 2
	units := make([]uint16, 0, n)
	for i := 0; i < n*2; i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return string(utf16.Decode(units))
}

// ── user data dispatch ──────────────────────────────────────────────────────

func decodeUserData(b []byte, udl int, dcs byte, hasUDH bool) (string, *ConcatInfo, error) {
	// DCS general data coding (TS 23.038 §4):
	//   0x00 = GSM 7-bit default
	//   0x04 = 8-bit data (binary)
	//   0x08 = UCS2
	// We treat anything matching the UCS2 group bits (DCS & 0x0C == 0x08) as UCS2,
	// anything matching the 8-bit binary bits as binary (rendered as hex), and
	// everything else as GSM 7-bit.
	udhBytes := 0
	var concat *ConcatInfo
	if hasUDH {
		var err error
		udhBytes, concat, err = parseUDH(b)
		if err != nil {
			return "", nil, err
		}
	}
	switch {
	case dcs&0x0C == 0x08:
		if len(b) < udl {
			return "", nil, errors.New("ucs2 UD truncated")
		}
		if udhBytes > udl {
			return "", nil, errors.New("ucs2 UDH exceeds UD length")
		}
		return decodeUCS2(b[udhBytes:udl]), concat, nil
	case dcs&0x0C == 0x04:
		if len(b) < udl {
			return "", nil, errors.New("8-bit UD truncated")
		}
		if udhBytes > udl {
			return "", nil, errors.New("8-bit UDH exceeds UD length")
		}
		return strings.ToUpper(hex.EncodeToString(b[udhBytes:udl])), concat, nil
	default:
		// 7-bit septets — udl is in septets, octets is ceil(udl*7/8).
		need := (udl*7 + 7) / 8
		if len(b) < need {
			return "", nil, errors.New("gsm-7 UD truncated")
		}
		if !hasUDH {
			return decodeGSM7(b[:need], udl), nil, nil
		}
		fillBits := (7 - ((udhBytes * 8) % 7)) % 7
		textBitOffset := udhBytes*8 + fillBits
		skipSeptets := textBitOffset / 7
		if skipSeptets > udl {
			return "", nil, errors.New("gsm-7 UDH exceeds UD length")
		}
		return decodeGSM7FromBitOffset(b[:need], udl-skipSeptets, textBitOffset), concat, nil
	}
}

func parseUDH(b []byte) (int, *ConcatInfo, error) {
	if len(b) < 1 {
		return 0, nil, errors.New("UDH missing length")
	}
	udhl := int(b[0])
	end := 1 + udhl
	if len(b) < end {
		return 0, nil, errors.New("UDH truncated")
	}
	var concat *ConcatInfo
	for i := 1; i < end; {
		if i+2 > end {
			return 0, nil, errors.New("UDH IE truncated")
		}
		iei := b[i]
		iedl := int(b[i+1])
		i += 2
		if i+iedl > end {
			return 0, nil, errors.New("UDH IE data truncated")
		}
		ie := b[i : i+iedl]
		switch {
		case iei == 0x00 && iedl == 3:
			concat = &ConcatInfo{Ref: int(ie[0]), Total: int(ie[1]), Part: int(ie[2])}
		case iei == 0x08 && iedl == 4:
			ref := int(ie[0])<<8 | int(ie[1])
			concat = &ConcatInfo{Ref: ref, Total: int(ie[2]), Part: int(ie[3])}
		}
		i += iedl
	}
	if concat != nil && (concat.Total <= 1 || concat.Part < 1 || concat.Part > concat.Total) {
		concat = nil
	}
	return end, concat, nil
}
