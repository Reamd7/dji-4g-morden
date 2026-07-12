// Command qmiprobe determines which USB interface and transport model the DJI
// Baiwang modem (QDC507, PID 0x0125) uses for QMI signalling.
//
// The QDC507 is a DJI-customized EC25-class module. Standard EC25 docs place
// QMI on MI_04, but the QDC507 firmware may differ. This probe:
//
//  1. Queries the modem via AT (MI_02) to confirm firmware mode and PS state.
//  2. Tests QMICTL_SYNC_REQ (msg 0x0027) on MI_04 via:
//     - Model A: bulk OUT → bulk IN (GobiNet-style)
//     - Model B: EP0 control encapsulation (cdc-wdm/qmi_wwan-style)
//  3. Tests SYNC on MI_00 (the other FF/FF/FF interface) to rule out interface
//     swap. MI_00 claim uses a goroutine timeout — interfaces without WinUSB
//     hang on claim.
//
// This is the "phase 0" gate for plans/stage2.
//
// Usage:
//
//	mise exec -- go run ./cmd/qmiprobe
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/gousb"
)

const (
	vid = 0x2C7C
	pid = 0x0125

	ifaceAT  = 2 // MI_02 = AT command port (verified working, stage 1)
	ifaceQMI = 4 // MI_04 = expected QMI data channel

	collectWindow = 3 * time.Second
	claimTimeout  = 5 * time.Second // for MI_00 (may hang without WinUSB)
)

// qmictlSyncReq is a QMICTL_SYNC_REQ frame (msg 0x0027, no TLVs).
//
// Built per quectel-qmi-go's Packet.Marshal() (frame.go):
//
//	QMUX header (6B): IFType=01 | Length=0x000B(LE) | CtlFlags=00 | SvcType=00(CTL) | ClientID=00
//	CTL  header (6B): CtlFlags=00(request) | TxID=01 | MsgID=0x0027(LE) | Length=0x0000(LE)
//
// 12 bytes total. Length = frameSize - 1 = 11 = 0x000B (frame.go:406-407).
var qmictlSyncReq = []byte{
	0x01, 0x0B, 0x00, 0x00, 0x00, 0x00, // QMUX header
	0x00, 0x01, 0x27, 0x00, 0x00, 0x00, // CTL header (msg 0x0027, txID 1)
}

type endpointRead struct {
	label string
	n     int
	data  []byte
	err   error
}

func main() {
	ctx := gousb.NewContext()
	defer ctx.Close()

	dev, err := ctx.OpenDeviceWithVIDPID(vid, pid)
	if err != nil {
		log.Fatalf("open device: %v", err)
	}
	if dev == nil {
		log.Fatalf("device %04x:%04x not found", vid, pid)
	}
	defer dev.Close()
	dev.ControlTimeout = collectWindow

	manuf, _ := dev.Manufacturer()
	product, _ := dev.Product()
	fmt.Printf("Modem: %04x:%04x  %s / %s\n", vid, pid, manuf, product)
	fmt.Println("==========================================================")

	cfg, err := dev.Config(1)
	if err != nil {
		log.Fatalf("config 1: %v", err)
	}
	defer cfg.Close()

	// Phase 1: AT diagnostic on MI_02.
	atDiag(cfg)

	// Phase 2: QMI probe on MI_04.
	mi04Bulk, mi04Ctrl := probeMI04(dev, cfg)

	// Phase 3: SYNC on MI_00 (other FF/FF/FF interface — check for swap).
	mi00Bulk := probeMI00(cfg)

	// Summary.
	fmt.Println("==========================================================")
	fmt.Println("SUMMARY")
	fmt.Println("==========================================================")
	fmt.Printf("  MI_00 (FF/FF/FF, 2 EP): bulk SYNC   = %s\n", verdict(mi00Bulk))
	fmt.Printf("  MI_04 (FF/FF/FF, 3 EP): bulk SYNC   = %s\n", verdict(mi04Bulk))
	fmt.Printf("  MI_04 (FF/FF/FF, 3 EP): control ENC = %s\n", verdict(mi04Ctrl))
	fmt.Println()
	switch {
	case mi04Bulk:
		fmt.Println("→ Model A (bulk) works on MI_04. QMITransport uses bulk OUT/IN.")
	case mi04Ctrl:
		fmt.Println("→ Model B (control) works on MI_04. QMITransport uses EP0 encapsulation.")
	case mi00Bulk:
		fmt.Println("→ SYNC works on MI_00, not MI_04. Interfaces may be swapped on QDC507.")
		fmt.Println("  Next: test model B on MI_00 and determine RX path.")
	default:
		fmt.Println("→ No interface responded to QMI SYNC.")
		fmt.Println("  QDC507 firmware may not route QMI to USB (despite usbnet=0, CGATT=1).")
		fmt.Println("  Next: investigate AT-based QMI ($QCRMCALL) or WinUSB pipe config.")
	}
}

// probeMI04 tests both transport models on MI_04.
func probeMI04(dev *gousb.Device, cfg *gousb.Config) (bulkOK, ctrlOK bool) {
	fmt.Println("--- MI_04 QMI probe (class FF/FF/FF, 3 endpoints) ---")
	iface, err := cfg.Interface(ifaceQMI, 0)
	if err != nil {
		fmt.Printf("  claim MI_04 failed: %v\n", err)
		return false, false
	}
	defer iface.Close()

	s := iface.Setting
	fmt.Printf("  class=%v sub=%v proto=%v\n", s.Class, s.SubClass, s.Protocol)
	for _, ep := range s.Endpoints {
		fmt.Printf("  EP 0x%02x  %-3s  %-9s  maxPacket=%d\n",
			ep.Number, ep.Direction, ep.TransferType, ep.MaxPacketSize)
	}

	out, err := iface.OutEndpoint(0x05)
	if err != nil {
		fmt.Printf("  open OUT 0x05 failed: %v\n", err)
		return false, false
	}
	bulkIn, err := iface.InEndpoint(0x88)
	if err != nil {
		fmt.Printf("  open IN 0x88 failed: %v\n", err)
		return false, false
	}
	intrIn, err := iface.InEndpoint(0x89)
	if err != nil {
		fmt.Printf("  open intr IN 0x89 failed: %v\n", err)
	}

	// CRITICAL: Set DTR (CDC SetControlLineState) before any QMI communication.
	// Without this, the QDC507/MDM9x30 firmware will not respond to QMI requests.
	// Discovered from Linux qmi_wwan.c: QMI_MATCH_FF_FF_FF(0x2c7c, 0x0125) →
	// qmi_wwan_info_quirk_dtr → qmi_wwan_change_dtr(dev, true).
	// bmRequestType=0x21 (class/interface/OUT), bRequest=0x22 (SET_CONTROL_LINE_STATE),
	// wValue=0x0001 (DTR on), wIndex=interface number.
	fmt.Println("  [DTR] CDC SetControlLineState (DTR=1, wIndex=4)")
	if _, derr := dev.Control(0x21, 0x22, 0x0001, uint16(ifaceQMI), nil); derr != nil {
		fmt.Printf("      DTR set failed: %v\n", derr)
	} else {
		fmt.Println("      DTR set OK")
	}
	time.Sleep(500 * time.Millisecond) // let QMI service wake up

	// Model A: bulk.
	fmt.Println("  [A] bulk SYNC → EP 0x05 OUT, listen EP 0x88 + 0x89")
	reads := listenAll(bulkIn, intrIn, collectWindow)
	if n, werr := out.Write(qmictlSyncReq); werr != nil {
		fmt.Printf("      bulk OUT write failed (%d): %v\n", n, werr)
		drain(reads)
	} else {
		bulkOK = printReads(reads, "bulk write")
	}
	fmtResult("MI_04 model A", bulkOK)

	// Model B: control encapsulation.
	time.Sleep(300 * time.Millisecond)
	fmt.Println("  [B] EP0 control (wIndex=4): SEND_ENCAPSULATED → GET")
	reads = listenAll(bulkIn, intrIn, collectWindow)
	if n, cerr := dev.Control(0x21, 0x00, 0x0000, 4, qmictlSyncReq); cerr != nil {
		fmt.Printf("      SEND_ENCAPSULATED failed (%d): %v\n", n, cerr)
		drain(reads)
	} else {
		time.Sleep(200 * time.Millisecond)
		respBuf := make([]byte, 2048)
		nGet, gerr := dev.Control(0xA1, 0x01, 0x0000, 4, respBuf)
		if gerr != nil {
			fmt.Printf("      GET_ENCAPSULATED error: %v\n", gerr)
		} else if nGet > 0 {
			fmt.Printf("      ← GET %d bytes: %s\n", nGet, hexDump(respBuf[:nGet]))
		}
		ctrlOK = printReads(reads, "control SEND")
		if nGet > 0 && respBuf[0] == 0x01 {
			ctrlOK = true
		}
	}
	fmtResult("MI_04 model B", ctrlOK)

	// Padded bulk SYNC (512 bytes) — some USB gadgets require maxPacketSize
	// multiples and silently drop short packets.
	time.Sleep(300 * time.Millisecond)
	fmt.Println("  [A-pad] bulk SYNC padded to 512 bytes → EP 0x05 OUT")
	padded := make([]byte, 512)
	copy(padded, qmictlSyncReq)
	reads = listenAll(bulkIn, intrIn, collectWindow)
	if n, werr := out.Write(padded); werr != nil {
		fmt.Printf("      padded bulk OUT write failed (%d): %v\n", n, werr)
		drain(reads)
	} else {
		padOK := printReads(reads, "padded bulk write")
		if padOK {
			bulkOK = true
		}
		fmtResult("MI_04 model A (padded)", padOK)
	}
	fmt.Println()
	return bulkOK, ctrlOK
}

// probeMI00 tests SYNC on MI_00. Uses a goroutine timeout on the claim because
// MI_00 may have a non-WinUSB driver that causes cfg.Interface() to hang.
func probeMI00(cfg *gousb.Config) bool {
	fmt.Println("--- MI_00 SYNC probe (class FF/FF/FF, 2 endpoints) ---")

	type claimResult struct {
		iface *gousb.Interface
		err   error
	}
	ch := make(chan claimResult, 1)
	go func() {
		iface, err := cfg.Interface(0, 0)
		ch <- claimResult{iface, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			fmt.Printf("  claim MI_00 failed: %v (likely no WinUSB)\n", res.err)
			fmt.Println()
			return false
		}
		defer res.iface.Close()

		s := res.iface.Setting
		fmt.Printf("  class=%v sub=%v proto=%v\n", s.Class, s.SubClass, s.Protocol)
		for _, ep := range s.Endpoints {
			fmt.Printf("  EP 0x%02x  %-3s  %-9s  maxPacket=%d\n",
				ep.Number, ep.Direction, ep.TransferType, ep.MaxPacketSize)
		}

		out, err := res.iface.OutEndpoint(0x01)
		if err != nil {
			fmt.Printf("  open OUT 0x01 failed: %v\n", err)
			return false
		}
		in, err := res.iface.InEndpoint(0x81)
		if err != nil {
			fmt.Printf("  open IN 0x81 failed: %v\n", err)
			return false
		}

		fmt.Println("  [A] bulk SYNC → EP 0x01 OUT, listen EP 0x81")
		reads := listenAll(in, nil, collectWindow)
		if n, werr := out.Write(qmictlSyncReq); werr != nil {
			fmt.Printf("      bulk OUT write failed (%d): %v\n", n, werr)
			drain(reads)
			return false
		}
		ok := printReads(reads, "bulk write")
		fmtResult("MI_00 model A", ok)
		fmt.Println()
		return ok

	case <-time.After(claimTimeout):
		fmt.Printf("  claim MI_00 timed out after %s (likely no WinUSB on MI_00)\n", claimTimeout)
		fmt.Println()
		return false
	}
}

// atDiag queries the modem via MI_02.
func atDiag(cfg *gousb.Config) {
	fmt.Println("--- AT diagnostic on MI_02 ---")
	iface, err := cfg.Interface(ifaceAT, 0)
	if err != nil {
		fmt.Printf("  (cannot claim MI_02: %v — skipping)\n\n", err)
		return
	}
	defer iface.Close()

	out, err := iface.OutEndpoint(0x03)
	if err != nil {
		return
	}
	in, err := iface.InEndpoint(0x84)
	if err != nil {
		return
	}

	for _, cmd := range []string{"ATE0", "ATI", "AT+QCFG=\"usbnet\"", "AT+CGATT?", "AT$QCRMCALL?", "AT+CGDCONT?"} {
		fmt.Printf("→ %s\n", cmd)
		reads := listenSingle(in, time.Second)
		if _, err := out.Write([]byte(cmd + "\r\n")); err != nil {
			fmt.Printf("  write error: %v\n", err)
			drain(reads)
			continue
		}
		for r := range reads {
			if r.err != nil || r.n == 0 {
				continue
			}
			fmt.Printf("← %s\n", strings.TrimRight(string(r.data), "\r\n"))
		}
	}
	fmt.Println()
}

// listenAll starts background readers on bulk IN (+ interrupt IN if non-nil).
func listenAll(bulkIn, intrIn *gousb.InEndpoint, window time.Duration) <-chan endpointRead {
	ctx, cancel := context.WithTimeout(context.Background(), window)
	out := make(chan endpointRead, 8)
	var wg sync.WaitGroup

	readEP := func(label string, ep *gousb.InEndpoint) {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			n, err := ep.ReadContext(ctx, buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case out <- endpointRead{label: label, n: n, data: data}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					return
				}
				select {
				case <-time.After(100 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
		}
	}

	wg.Add(1)
	go readEP("bulk-IN", bulkIn)
	if intrIn != nil {
		wg.Add(1)
		go readEP("intr-IN", intrIn)
	}
	go func() {
		<-ctx.Done()
		cancel()
		wg.Wait()
		close(out)
	}()
	return out
}

// listenSingle starts a background reader on one IN endpoint.
func listenSingle(in *gousb.InEndpoint, window time.Duration) <-chan endpointRead {
	ctx, cancel := context.WithTimeout(context.Background(), window)
	out := make(chan endpointRead, 4)
	go func() {
		defer cancel()
		defer close(out)
		buf := make([]byte, 512)
		for {
			n, err := in.ReadContext(ctx, buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case out <- endpointRead{label: "AT", n: n, data: data}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					return
				}
				select {
				case <-time.After(100 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func printReads(reads <-chan endpointRead, afterMsg string) bool {
	gotQMI := false
	any := false
	for r := range reads {
		any = true
		if r.err != nil {
			continue
		}
		fmt.Printf("      ← %s %d bytes: %s\n", r.label, r.n, hexDump(r.data))
		if r.n > 0 && r.data[0] == 0x01 {
			gotQMI = true
		}
	}
	if !any {
		fmt.Printf("      (no data on any IN endpoint after %s)\n", afterMsg)
	}
	return gotQMI
}

func drain(reads <-chan endpointRead) {
	for range reads {
	}
}

func fmtResult(label string, ok bool) {
	if ok {
		fmt.Printf("  → %s: OK\n", label)
	} else {
		fmt.Printf("  → %s: no response\n", label)
	}
}

func verdict(ok bool) string {
	if ok {
		return "WORKS"
	}
	return "no response"
}

func hexDump(b []byte) string {
	const hex = "0123456789abcdef"
	var sb []byte
	sb = append(sb, '\'')
	for i, c := range b {
		if i > 0 {
			sb = append(sb, ' ')
		}
		sb = append(sb, hex[c>>4], hex[c&0x0f])
	}
	sb = append(sb, '\'', ' ', '[')
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb = append(sb, c)
		} else {
			sb = append(sb, '.')
		}
	}
	sb = append(sb, ']')
	return string(sb)
}
