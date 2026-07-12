// Command bulkprobe is the stage 3 Phase 0 data-path probe. It verifies the
// two gating risks for the TUN relay approach:
//
//  1. WDA SetDataFormat succeeds on QDC507 (R2) — raw-IP mode negotiated
//  2. Bulk IN EP 0x88 carries raw IP packets after WDS StartNetwork (R1)
//
// Additionally tests ZLP behavior (R5): does the modem need a Zero Length
// Packet after TX URBs that are multiples of 512 bytes?
//
// The probe is progressive — each phase prints results before continuing:
//
//	Phase A: WDA allocation + SetDataFormat (LinkProtocol=0x02)
//	Phase B: WDS StartNetwork (dial, get IP + PDH)
//	Phase C: Read bulk IN EP 0x88 for 8s, check IP version nibble
//	Phase D: If no data, send ICMP echo via bulk OUT, read reply
//	Phase D2: ZLP test — send exactly 512-byte packet, check if stuck
//
// Usage:
//
//	mise exec -- go run ./cmd/bulkprobe
//	mise exec -- go run ./cmd/bulkprobe -apn wonet
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"dji-modem-research/internal/qmitransport"
	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

func main() {
	apn := flag.String("apn", "3gnet", "APN for dialup")
	flag.Parse()

	ctx := context.Background()

	// ── Phase A: transport + client + WDA allocation ─────────────────────

	fmt.Println("[Phase A] Opening QMITransport + QMI client + manager (WDA allocation)...")
	transport, err := qmitransport.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Open transport failed: %v\n", err)
		os.Exit(1)
	}
	defer transport.Close()
	fmt.Println("  OK — MI_04 claimed, DTR set")

	clientOpts := qmi.DefaultClientOptions()
	clientOpts.Logf = func(level qmi.ClientLogLevel, format string, args ...any) {
		prefix := "DEBUG"
		if level == qmi.ClientLogLevelWarn {
			prefix = "WARN"
		} else if level == qmi.ClientLogLevelError {
			prefix = "ERROR"
		}
		fmt.Printf("  [qmi:%s] %s\n", prefix, fmt.Sprintf(format, args...))
	}
	client, err := qmi.NewClientFromTransport(ctx, transport, clientOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  NewClientFromTransport failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Println("  OK — QMI client running")

	// NetInterface="dummy" triggers shouldAllocateWDA() → WDA allocated +
	// enableRawIP() → wda.SetDataFormat(LinkProtocolIP). On Windows the
	// sysfs path is skipped (GOOS != linux); only the modem-side SetDataFormat
	// runs, which is exactly what we need.
	cfg := manager.Config{
		Device:     manager.ModemDevice{NetInterface: "dummy"},
		APN:        *apn,
		EnableIPv4: true,
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
	defer startCancel()
	if err := mgr.StartCoreContext(startCtx); err != nil {
		fmt.Fprintf(os.Stderr, "  StartCore failed: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Stop()
	fmt.Println("  OK — StartCore complete (WDA should be allocated, raw-IP set)")

	// ── Phase B: WDS StartNetwork (dial) ─────────────────────────────────

	fmt.Printf("\n[Phase B] WDS StartNetwork (APN=%s)...\n", *apn)
	if err := mgr.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "  Connect failed: %v\n", err)
		os.Exit(1)
	}

	s := mgr.Settings()
	if s == nil || len(s.IPv4Address) == 0 {
		fmt.Fprintf(os.Stderr, "  No IPv4 address after Connect\n")
		os.Exit(1)
	}
	srcIP := s.IPv4Address
	fmt.Printf("  OK — IP=%s, GW=%s, MTU=%d, PDH=0x%08x\n",
		srcIP, s.IPv4Gateway, s.MTU, mgr.HandleV4())
	fmt.Printf("  DNS: %s, %s\n", s.IPv4DNS1, s.IPv4DNS2)

	// ── Phase C: read bulk IN EP 0x88 ────────────────────────────────────

	fmt.Println("\n[Phase C] Reading bulk IN EP 0x88 for 8s...")
	bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  OpenBulkEndpoints failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  OK — bulk IN 0x88 + bulk OUT 0x05 opened")

	readCtx, readCancel := context.WithTimeout(ctx, 8*time.Second)
	defer readCancel()

	buf := make([]byte, 65535)
	count := 0
	var lastVersion byte // IP version nibble of last successfully-read packet
	for {
		n, err := bulkIn.ReadContext(readCtx, buf)
		if err != nil {
			break // context timeout
		}
		if n == 0 {
			continue
		}
		pkt := buf[:n]
		version := pkt[0] >> 4
		lastVersion = version
		fmt.Printf("  bulk IN: %d bytes, IP version=%d, first 20 bytes: % x\n",
			n, version, pkt[:min(20, n)])
		count++
	}
	fmt.Printf("  Read %d packets in 8s\n", count)

	if count > 0 {
		if lastVersion == 4 || lastVersion == 6 {
			fmt.Println("\n✓ bulk EP carries raw IP data — stage 3 relay is viable")
		} else if lastVersion <= 7 {
			fmt.Println("\n⚠ bulk EP data looks like QMAP (mux_id header) — need strip/add layer")
		}
	} else {
		fmt.Println("\n  No data received — trying Phase D (trigger traffic)")
	}


	if count == 0 {
		fmt.Println("\n[Phase D] Sending ICMP echo via bulk OUT EP 0x05...")
		dstIP := "114.114.114.114"
		icmpPkt := buildICMPEcho(srcIP.String(), dstIP, 0)
		fmt.Printf("  Sending %d-byte ICMP echo to %s\n", len(icmpPkt), dstIP)

		writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
		n, err := bulkOut.WriteContext(writeCtx, icmpPkt)
		writeCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  bulk OUT write failed: %v\n", err)
		} else {
			fmt.Printf("  Wrote %d bytes to bulk OUT\n", n)
		}

		// Read reply
		replyCtx, replyCancel := context.WithTimeout(ctx, 5*time.Second)
		defer replyCancel()
		n, err = bulkIn.ReadContext(replyCtx, buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  No reply (timeout): %v\n", err)
		} else if n > 0 {
			version := buf[0] >> 4
			fmt.Printf("  Reply: %d bytes, IP version=%d, first 20 bytes: % x\n",
				n, version, buf[:min(20, n)])
			if version == 4 || version == 6 {
				fmt.Println("✓ Got raw IP reply — relay viable (modem responds to upstream)")
			}
		}
	}

	// ── Phase D2: ZLP test (R5 verification) ─────────────────────────────

	fmt.Println("\n[Phase D2] ZLP test — sending exactly 512-byte packet...")
	dstIP := "114.114.114.114"
	// IP header = 20, ICMP header = 8, payload = 512 - 20 - 8 = 484
	needed := 512 - 20 - 8
	pkt512 := buildICMPEcho(srcIP.String(), dstIP, needed)
	fmt.Printf("  Sending %d-byte packet (no ZLP appended)\n", len(pkt512))

	zlpCtx, zlpCancel := context.WithTimeout(ctx, 5*time.Second)
	n, err := bulkOut.WriteContext(zlpCtx, pkt512)
	zlpCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  bulk OUT write failed: %v\n", err)
	} else {
		fmt.Printf("  Wrote %d bytes\n", n)
	}

	replyCtx2, replyCancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer replyCancel2()
	n, err = bulkIn.ReadContext(replyCtx2, buf)
	if err != nil {
		fmt.Printf("  No reply within 5s — modem may need ZLP for 512-multiple packets\n")
		fmt.Println("  → relay should set zlp=true (append 0-byte Write after len%512==0)")
	} else if n > 0 {
		version := buf[0] >> 4
		fmt.Printf("  Reply: %d bytes, IP version=%d\n", n, version)
		fmt.Println("  → modem does NOT require ZLP (reply received without ZLP)")
		fmt.Println("  → relay can set zlp=false")
	}

	fmt.Println("\nDone.")
}

// buildICMPEcho constructs a raw IPv4 ICMP echo request packet.
// payloadLen is the number of padding bytes in the ICMP payload.
func buildICMPEcho(srcIP, dstIP string, payloadLen int) []byte {
	// ICMP echo request: type=8, code=0, checksum, id=1, seq=1, payload
	icmpLen := 8 + payloadLen
	icmpBytes := make([]byte, icmpLen)
	icmpBytes[0] = 8 // type = echo request
	icmpBytes[1] = 0 // code
	binary.BigEndian.PutUint16(icmpBytes[4:6], 1) // ID
	binary.BigEndian.PutUint16(icmpBytes[6:8], 1) // Seq
	for i := 8; i < icmpLen; i++ {
		icmpBytes[i] = byte(i)
	}
	// ICMP checksum
	cs := checksum(icmpBytes)
	binary.BigEndian.PutUint16(icmpBytes[2:4], cs)

	// IPv4 header (20 bytes, no options)
	totalLen := 20 + icmpLen
	hdr := make([]byte, 20)
	hdr[0] = 0x45 // version=4, IHL=5 (20 bytes)
	hdr[1] = 0x00 // DSCP/ECN
	binary.BigEndian.PutUint16(hdr[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(hdr[4:6], 1) // identification
	binary.BigEndian.PutUint16(hdr[6:8], 0) // flags + fragment offset
	hdr[8] = 64                              // TTL
	hdr[9] = 1                               // protocol = ICMP
	// hdr[10:12] = checksum (computed below)
	src := net.ParseIP(srcIP).To4()
	dst := net.ParseIP(dstIP).To4()
	copy(hdr[12:16], src)
	copy(hdr[16:20], dst)
	cs = checksum(hdr)
	binary.BigEndian.PutUint16(hdr[10:12], cs)

	return append(hdr, icmpBytes...)
}

// checksum computes the Internet checksum (RFC 1071) for a byte slice.
func checksum(b []byte) uint16 {
	sum := 0
	for i := 0; i+1 < len(b); i += 2 {
		sum += int(b[i])<<8 | int(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += int(b[len(b)-1]) << 8
	}
	for sum>>16 > 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
