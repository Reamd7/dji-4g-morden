// Package modem — SMS PDU 编解码 facade。
//
// 历史上本文件是 sms_gateway 手写的 TS 23.040 + TS 23.038 子集(路线 A),
// 有三个已知缺陷:无 GSM-7 扩展表、发送只 8-bit ref 不自动分段、接收不重组。
//
// 2026-07-12 升级到路线 B(SG 壳 + smscodec 芯):手写编解码全部删除,
// DecodeDeliver 委托 third_party/smscodec(基于 warthog618/sms,Go 生态标准
// 符合度最高的 TS 23.040 实现),三个缺陷一并消除。发送侧的 PDU 编码也走
// smscodec.BuildSubmitTPDUs(自动分段),见 encodeSubmitPDUs。
//
// 本文件只保留对外的聚合类型(DecodedSMS / ConcatInfo)和两个薄函数,
// 把 warthog618 依赖藏在包内、不泄露到公共 API。AT 框架壳(modem.go)不动。
package modem

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"dji-modem-research/third_party/smscodec"
)

// DecodedSMS is the parsed view of one SMS-DELIVER PDU.
type DecodedSMS struct {
	Sender    string
	Body      string
	Timestamp time.Time
	RawPDU    string // upper-case hex of the original PDU
	Concat    *ConcatInfo
}

// ConcatInfo carries UDH concatenation metadata for a long-SMS segment.
// nil means single-part (no UDH concat header).
type ConcatInfo struct {
	Ref   int
	Part  int // smscodec reports this as Seq; we expose it as Part for callers
	Total int
}

// DecodeDeliver parses a hex-encoded SMS-DELIVER as returned by AT+CMGL=4 /
// AT+CMGR. It first strips the leading SCA (SMSC) header, then delegates the
// bare TPDU to smscodec.DecodeDeliverTPDU, which handles GSM-7 (incl.
// extension table), UCS-2, UDH concat metadata, SCTS timestamps, and
// national-modem PDU tolerances (spare-bit normalization, length trimming).
func DecodeDeliver(hexPDU string) (*DecodedSMS, error) {
	hexPDU = strings.TrimSpace(hexPDU)
	raw, err := hex.DecodeString(hexPDU)
	if err != nil {
		return nil, fmt.Errorf("pdu hex: %w", err)
	}
	// Strip the SCA (SMSC) header: the first octet is the SCA length (in
	// octets of the address that follows); the bare TPDU starts after it.
	// DecodeDeliverTPDU wraps warthog618's sms.Unmarshal, which expects a
	// bare TPDU, not a full AT PDU. Mirrors vohive manager.go:1664-1670.
	if len(raw) > 0 {
		scaLen := int(raw[0])
		if len(raw) > scaLen+1 {
			raw = raw[scaLen+1:]
		}
	}
	sender, text, ts, concat, err := smscodec.DecodeDeliverTPDU(raw)
	if err != nil {
		return nil, err
	}
	d := &DecodedSMS{
		Sender:    sender,
		Body:      text,
		Timestamp: ts,
		RawPDU:    strings.ToUpper(hexPDU),
	}
	if concat.IsConcat {
		d.Concat = &ConcatInfo{Ref: concat.Ref, Part: concat.Seq, Total: concat.Total}
	}
	return d, nil
}

// submitSegment is one AT+CMGS unit: the hex PDU to write (with the leading
// SCA placeholder byte) and the TPDU length that AT+CMGS=<len> expects
// (octets after the SCA byte).
type submitSegment struct {
	hexPDU  string
	tpduLen int
}

// encodeSubmitPDUs encodes an SMS-SUBMIT, auto-splitting into multiple
// segments when the body exceeds one PDU (>160 GSM-7 septets or >70 UCS-2
// code units). Each segment is returned ready for one AT+CMGS round-trip:
// hexPDU carries a leading "00" SCA placeholder ("use the SMSC stored on
// SIM", matching the legacy EncodeSubmit behaviour) and tpduLen is the bare
// TPDU length (what AT+CMGS=<len> wants).
func encodeSubmitPDUs(recipient, body string) ([]submitSegment, error) {
	tpdus, _, err := smscodec.BuildSubmitTPDUs(recipient, body)
	if err != nil {
		return nil, fmt.Errorf("encode submit: %w", err)
	}
	segs := make([]submitSegment, 0, len(tpdus))
	for _, tpdu := range tpdus {
		segs = append(segs, submitSegment{
			hexPDU:  "00" + hex.EncodeToString(tpdu),
			tpduLen: len(tpdu),
		})
	}
	return segs, nil
}
