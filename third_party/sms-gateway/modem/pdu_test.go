package modem

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/warthog618/sms/encoding/tpdu"
)

// buildDeliverPDU builds a full SMS-DELIVER PDU (SCA placeholder + TPDU) as a
// hex string, the same shape AT+CMGL/+CMGR returns. It is the inverse of
// DecodeDeliver's input, so tests round-trip through warthog618 itself — the
// point is to exercise the facade plumbing (hex→bytes, field mapping,
// ConcatInfo Seq→Part), not to re-verify warthog618's codec.
func buildDeliverPDU(t *testing.T, sender, body string, scts time.Time) string {
	t.Helper()
	d, err := tpdu.NewDeliver()
	if err != nil {
		t.Fatalf("NewDeliver: %v", err)
	}
	ud, udh, alpha := tpdu.EncodeUserData([]byte(body))
	d.OA = tpdu.NewAddress(tpdu.FromNumber(sender))
	d.SCTS = tpdu.Timestamp{Time: scts}
	d.UD = ud
	if udh != nil {
		d.UDH = udh
	}
	switch alpha {
	case tpdu.AlphaUCS2:
		d.DCS = 0x08
	default:
		d.DCS = 0x00
	}
	tpduBytes, err := d.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	// Prepend the SCA placeholder (00 = use SMSC on SIM) like a real AT PDU.
	return "00" + strings.ToUpper(hex.EncodeToString(tpduBytes))
}

func TestDecodeDeliver_GSM7Basic(t *testing.T) {
	scts := time.Date(2026, 7, 12, 10, 30, 0, 0, time.FixedZone("SCTS", 8*3600))
	pdu := buildDeliverPDU(t, "+8613800000001", "Hello", scts)

	got, err := DecodeDeliver(pdu)
	if err != nil {
		t.Fatalf("DecodeDeliver: %v", err)
	}
	if got.Sender != "+8613800000001" {
		t.Errorf("Sender = %q, want +8613800000001", got.Sender)
	}
	if got.Body != "Hello" {
		t.Errorf("Body = %q, want Hello", got.Body)
	}
	if got.Concat != nil {
		t.Errorf("Concat = %v, want nil for single-part", got.Concat)
	}
	if got.RawPDU != pdu {
		t.Errorf("RawPDU mismatch: got %q want %q", got.RawPDU, pdu)
	}
}

// TestDecodeDeliver_GSM7Extension is the headline defect fix: the old
// hand-rolled pdu.go mapped every GSM-7 extension char (^{}[]~|\€) to '?'.
// With smscodec the full TS 23.038 §6.2.1 extension table is honoured.
func TestDecodeDeliver_GSM7Extension(t *testing.T) {
	extChars := "^{}[]~|\\€" // all live in the GSM-7 extension table
	scts := time.Date(2026, 7, 12, 10, 30, 0, 0, time.FixedZone("SCTS", 8*3600))
	pdu := buildDeliverPDU(t, "+8613800000001", extChars, scts)

	got, err := DecodeDeliver(pdu)
	if err != nil {
		t.Fatalf("DecodeDeliver: %v", err)
	}
	if got.Body != extChars {
		t.Errorf("Body = %q, want %q (extension chars must not degrade to '?')", got.Body, extChars)
	}
}

func TestDecodeDeliver_UCS2Chinese(t *testing.T) {
	scts := time.Date(2026, 7, 12, 10, 30, 0, 0, time.FixedZone("SCTS", 8*3600))
	pdu := buildDeliverPDU(t, "+8613800000001", "测试中文短信", scts)

	got, err := DecodeDeliver(pdu)
	if err != nil {
		t.Fatalf("DecodeDeliver: %v", err)
	}
	if got.Body != "测试中文短信" {
		t.Errorf("Body = %q, want 测试中文短信", got.Body)
	}
}

func TestDecodeDeliver_BadHex(t *testing.T) {
	if _, err := DecodeDeliver("not-hex-zz"); err == nil {
		t.Error("expected error for bad hex, got nil")
	}
}

func TestDecodeDeliver_MalformedTPDU(t *testing.T) {
	// Valid hex, SCA "00" stripped, but the remaining TPDU is garbage → the
	// underlying decoder must surface an error rather than panic.
	if _, err := DecodeDeliver("00FF"); err == nil {
		t.Error("expected error for malformed TPDU, got nil")
	}
}

func TestEncodeSubmitPDUs_EmptyBodyError(t *testing.T) {
	// Empty body → smscodec yields no TPDU → encodeSubmitPDUs must surface the error.
	if _, err := encodeSubmitPDUs("+8613800000001", ""); err == nil {
		t.Error("expected error for empty body, got nil")
	}
}

func TestEncodeSubmitPDUs_SingleSegmentSCAPrefix(t *testing.T) {
	segs, err := encodeSubmitPDUs("+8613800000001", "hello")
	if err != nil {
		t.Fatalf("encodeSubmitPDUs: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1", len(segs))
	}
	seg := segs[0]
	// AT+CMGS PDU must start with the 00 SCA placeholder ("use SMSC on SIM").
	if !strings.HasPrefix(seg.hexPDU, "00") {
		t.Errorf("hexPDU = %q, want leading '00' SCA placeholder", seg.hexPDU)
	}
	// tpduLen is the bare TPDU length (hexPDU minus the 2-hex-byte SCA, /2).
	wantLen := (len(seg.hexPDU) - 2) / 2
	if seg.tpduLen != wantLen {
		t.Errorf("tpduLen = %d, want %d", seg.tpduLen, wantLen)
	}
}

func TestEncodeSubmitPDUs_LongBodyMultiSegment(t *testing.T) {
	// 200 ASCII chars > 160 GSM-7 septet limit → must auto-split into ≥2 segments.
	long := strings.Repeat("A", 200)
	segs, err := encodeSubmitPDUs("+8613800000001", long)
	if err != nil {
		t.Fatalf("encodeSubmitPDUs: %v", err)
	}
	if len(segs) < 2 {
		t.Fatalf("segments = %d, want ≥2 for 200-char body (auto-split)", len(segs))
	}
	// Every segment must carry the SCA placeholder + a consistent tpduLen.
	for i, seg := range segs {
		if !strings.HasPrefix(seg.hexPDU, "00") {
			t.Errorf("seg %d hexPDU = %q, want leading '00'", i, seg.hexPDU)
		}
		wantLen := (len(seg.hexPDU) - 2) / 2
		if seg.tpduLen != wantLen {
			t.Errorf("seg %d tpduLen = %d, want %d", i, seg.tpduLen, wantLen)
		}
	}
}

func TestEncodeSubmitPDUs_ChineseAutoUCS2(t *testing.T) {
	segs, err := encodeSubmitPDUs("+8613800000001", "测试")
	if err != nil {
		t.Fatalf("encodeSubmitPDUs: %v", err)
	}
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1 for short UCS2", len(segs))
	}
	// Decode the TPDU back and confirm it was encoded as UCS-2.
	raw, err := hex.DecodeString(segs[0].hexPDU)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	d := &tpdu.TPDU{Direction: tpdu.MO}
	if err := d.UnmarshalBinary(raw[1:]); err != nil { // skip SCA 00 byte
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	alpha, err := d.DCS.Alphabet()
	if err != nil {
		t.Fatalf("Alphabet: %v", err)
	}
	if alpha != tpdu.AlphaUCS2 {
		t.Errorf("DCS alphabet = %v, want UCS2 for Chinese body", alpha)
	}
}
