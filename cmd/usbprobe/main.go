// Command usbprobe enumerates the DJI Baiwang 4G modem (Quectel EC25 PID
// 2C7C:0125) USB interfaces and dumps their endpoint addresses.
//
// This resolves the open question in docs/01 §8.1: the EC25-mode endpoint
// layout (bulk IN/OUT addresses for MI_02 AT port and MI_04 QMI data channel)
// is not documented anywhere and must be observed at runtime.
//
// The descriptor formatting logic lives in internal/usbdesc (pure, unit-tested);
// this command only handles the hardware-dependent device opening.
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/google/gousb"

	"dji-modem-research/internal/usbdesc"
)

const (
	vid = 0x2C7C // Quectel Wireless Solutions
	pid = 0x0125 // EC25 LTE modem (standard PID after flashing from DJI 2CA3:4006)
)

func main() {
	ctx := gousb.NewContext()
	defer ctx.Close()

	dev, err := ctx.OpenDeviceWithVIDPID(vid, pid)
	if err != nil {
		log.Fatalf("Failed to open device %04x:%04x: %v", vid, pid, err)
	}
	if dev == nil {
		log.Fatalf("Device %04x:%04x not found. Is it plugged in?", vid, pid)
	}
	defer dev.Close()

	manuf, _ := dev.Manufacturer()
	product, _ := dev.Product()
	serial, _ := dev.SerialNumber()

	fmt.Printf("DJI Baiwang modem found: %04x:%04x\n", vid, pid)
	fmt.Printf("  iManufacturer: %s\n", manuf)
	fmt.Printf("  iProduct:      %s\n", product)
	fmt.Printf("  iSerial:       %s\n", serial)
	fmt.Println(strings.Repeat("=", 70))

	for cfgNum, cfg := range dev.Desc.Configs {
		fmt.Printf("\nConfiguration %d\n", cfgNum)
		ifaces := usbdesc.FromConfig(cfg)
		fmt.Print(usbdesc.Render(ifaces))
	}
}
