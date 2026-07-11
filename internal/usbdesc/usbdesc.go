// Package usbdesc formats USB interface and endpoint descriptors into stable
// human-readable strings. The formatting logic is pure (no I/O), pulled out of
// cmd/usbprobe so it can be unit-tested without real hardware.
package usbdesc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/gousb"
)

// EndpointInfo is a serializable view of a USB endpoint descriptor, decoupled
// from gousb so tests can construct values without opening a device.
type EndpointInfo struct {
	Address       uint8
	Direction     gousb.EndpointDirection
	TransferType  gousb.TransferType
	MaxPacketSize int
}

// InterfaceInfo is a serializable view of one USB interface (alt setting 0).
type InterfaceInfo struct {
	Number    uint8
	Class     uint8
	SubClass  uint8
	Protocol  uint8
	Alternate uint8
	Endpoints []EndpointInfo
}

// DescribeDirection returns the canonical 3-char direction label ("IN " or
// "OUT") used in probe output.
func DescribeDirection(d gousb.EndpointDirection) string {
	if d == gousb.EndpointDirectionIn {
		return "IN "
	}
	return "OUT"
}

// DescribeTransferType returns the 4-char transfer-type label
// ("bulk", "intr", "iso ", "ctrl").
func DescribeTransferType(t gousb.TransferType) string {
	switch t {
	case gousb.TransferTypeInterrupt:
		return "intr"
	case gousb.TransferTypeIsochronous:
		return "iso "
	case gousb.TransferTypeControl:
		return "ctrl"
	default:
		return "bulk"
	}
}

// FormatEndpoint renders a single endpoint line, e.g.:
//
//	EP 0x03  OUT  bulk  maxPacket=512
func FormatEndpoint(ep EndpointInfo) string {
	return fmt.Sprintf("EP 0x%02x  %s  %-4s  maxPacket=%d",
		ep.Address, DescribeDirection(ep.Direction),
		DescribeTransferType(ep.TransferType), ep.MaxPacketSize)
}

// FormatInterface renders the interface header line, e.g.:
//
//	Interface 2 (MI_02)  class=ff sub=00 proto=00  alt=0
func FormatInterface(iface InterfaceInfo) string {
	return fmt.Sprintf("Interface %d (MI_%02d)  class=%02x sub=%02x proto=%02x  alt=%d",
		iface.Number, iface.Number, iface.Class, iface.SubClass, iface.Protocol, iface.Alternate)
}

// SortEndpoints returns endpoint addresses sorted ascending. Useful for stable
// output independent of map iteration order.
func SortEndpoints(eps []EndpointInfo) []EndpointInfo {
	sorted := make([]EndpointInfo, len(eps))
	copy(sorted, eps)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Address < sorted[j].Address
	})
	return sorted
}

// FromConfig extracts InterfaceInfo slices from a gousb config descriptor, with
// interfaces sorted by number and endpoints sorted by address. This is the
// hardware-dependent entry point; everything downstream is pure.
func FromConfig(cfg gousb.ConfigDesc) []InterfaceInfo {
	// ConfigDesc.Interfaces is []InterfaceDesc; copy and sort by Number.
	ifaces := make([]gousb.InterfaceDesc, len(cfg.Interfaces))
	copy(ifaces, cfg.Interfaces)
	sort.Slice(ifaces, func(i, j int) bool {
		return ifaces[i].Number < ifaces[j].Number
	})

	out := make([]InterfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		if len(iface.AltSettings) == 0 {
			out = append(out, InterfaceInfo{Number: uint8(iface.Number)})
			continue
		}
		alt := iface.AltSettings[0]
		eps := make([]EndpointInfo, 0, len(alt.Endpoints))
		for addr, ep := range alt.Endpoints {
			eps = append(eps, EndpointInfo{
				Address:       uint8(addr),
				Direction:     ep.Direction,
				TransferType:  ep.TransferType,
				MaxPacketSize: ep.MaxPacketSize,
			})
		}
		eps = SortEndpoints(eps)
		out = append(out, InterfaceInfo{
			Number:    uint8(alt.Number),
			Class:     uint8(alt.Class),
			SubClass:  uint8(alt.SubClass),
			Protocol:  uint8(alt.Protocol),
			Alternate: uint8(alt.Alternate),
			Endpoints: eps,
		})
	}
	return out
}

// Render produces the full text dump of all interfaces in a config, matching
// the historical cmd/usbprobe output format.
func Render(ifaces []InterfaceInfo) string {
	var b strings.Builder
	for _, iface := range ifaces {
		fmt.Fprintf(&b, "  %s\n", FormatInterface(iface))
		if len(iface.Endpoints) == 0 {
			b.WriteString("    (no endpoints)\n")
			continue
		}
		for _, ep := range iface.Endpoints {
			fmt.Fprintf(&b, "    %s\n", FormatEndpoint(ep))
		}
	}
	return b.String()
}
