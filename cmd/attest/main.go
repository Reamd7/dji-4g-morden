// Command attest verifies the MI_02 AT command channel by sending AT and
// reading the response. This proves the full gousb → libusb → WinUSB → modem
// path works end-to-end before investing in the USB transport layer.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/gousb"
)

const (
	vid = 0x2C7C
	pid = 0x0125

	ifaceAT = 2 // MI_02 = AT command port
	epOut   = 0x03
	epIn    = 0x84
)

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

	// Activate config 1 and claim MI_02.
	cfg, err := dev.Config(1)
	if err != nil {
		log.Fatalf("config 1: %v", err)
	}
	defer cfg.Close()

	iface, err := cfg.Interface(ifaceAT, 0)
	if err != nil {
		log.Fatalf("claim interface %d: %v", ifaceAT, err)
	}
	defer iface.Close()

	out, err := iface.OutEndpoint(epOut)
	if err != nil {
		log.Fatalf("open OUT endpoint 0x%02x: %v", epOut, err)
	}
	in, err := iface.InEndpoint(epIn)
	if err != nil {
		log.Fatalf("open IN endpoint 0x%02x: %v", epIn, err)
	}

	// Send AT\r\n
	cmd := []byte("AT\r\n")
	if n, err := out.Write(cmd); err != nil {
		log.Fatalf("write AT: %v (wrote %d)", err, n)
	}
	fmt.Printf("→ sent %q (%d bytes)\n", string(cmd), len(cmd))

	// Read response. Modem typically echoes "AT\r\n" then "\r\nOK\r\n".
	// Poll the IN endpoint a few times within a deadline.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		buf := make([]byte, 512)
		n, err := in.ReadContext(rctx, buf)
		cancel()
		if n > 0 {
			fmt.Printf("← recv %d bytes: %q\n", n, string(buf[:n]))
		}
		if err != nil && err != context.DeadlineExceeded {
			fmt.Printf("(read error: %v)\n", err)
		}
	}
	fmt.Println("Done.")
}
