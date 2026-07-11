// Command usbprobe enumerates the DJI Baiwang 4G modem (Quectel EC25 PID
// 2C7C:0125) USB interfaces and dumps their endpoint addresses.
//
// This resolves the open question in docs/01 §8.1: the EC25-mode endpoint
// layout (bulk IN/OUT addresses for MI_02 AT port and MI_04 QMI data channel)
// is not documented anywhere and must be observed at runtime.
package main

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/google/gousb"
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

	// Enumerate via the device descriptor (no need to claim/configure).
	for cfgNum, cfg := range dev.Desc.Configs {
		fmt.Printf("\nConfiguration %d\n", cfgNum)
		// Sort interfaces by number for stable output.
		ifaceNums := make([]int, 0, len(cfg.Interfaces))
		for n := range cfg.Interfaces {
			ifaceNums = append(ifaceNums, int(n))
		}
		sort.Ints(ifaceNums)

		for _, n := range ifaceNums {
			iface := cfg.Interfaces[uint8(n)]
			if len(iface.AltSettings) == 0 {
				fmt.Printf("  Interface %d (MI_%02d): no alt settings\n", n, n)
				continue
			}
			// Alt setting 0 is the default.
			alt := iface.AltSettings[0]
			fmt.Printf("  Interface %d (MI_%02d)  class=%02x sub=%02x proto=%02x  alt=%d\n",
				n, n, uint8(alt.Class), uint8(alt.SubClass), uint8(alt.Protocol), alt.Alternate)

			// Endpoints is a map[EndpointAddress]EndpointDesc — sort by address.
			epAddrs := make([]int, 0, len(alt.Endpoints))
			for a := range alt.Endpoints {
				epAddrs = append(epAddrs, int(a))
			}
			sort.Ints(epAddrs)

			if len(epAddrs) == 0 {
				fmt.Println("    (no endpoints)")
				continue
			}
			for _, a := range epAddrs {
				ep := alt.Endpoints[EndpointAddress(a)]
				dir := "OUT"
				if ep.Direction == gousb.EndpointDirectionIn {
					dir = "IN "
				}
				transfer := "bulk"
				switch ep.TransferType {
				case gousb.TransferTypeInterrupt:
					transfer = "intr"
				case gousb.TransferTypeIsochronous:
					transfer = "iso "
				case gousb.TransferTypeControl:
					transfer = "ctrl"
				}
				fmt.Printf("    EP 0x%02x  %s  %-4s  maxPacket=%d\n",
					a, dir, transfer, ep.MaxPacketSize)
			}
		}
	}
}

// EndpointAddress is re-exported here only to satisfy the type system in the
// map iteration above; gousb defines it as type EndpointAddress uint8.
type EndpointAddress = gousb.EndpointAddress
