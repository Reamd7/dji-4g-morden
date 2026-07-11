package usbdesc

import (
	"strings"
	"testing"

	"github.com/google/gousb"
)

func TestDescribeDirection(t *testing.T) {
	tests := []struct {
		name string
		in   gousb.EndpointDirection
		want string
	}{
		{"in", gousb.EndpointDirectionIn, "IN "},
		{"out", gousb.EndpointDirectionOut, "OUT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DescribeDirection(tc.in); got != tc.want {
				t.Errorf("DescribeDirection(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDescribeTransferType(t *testing.T) {
	tests := []struct {
		name string
		in   gousb.TransferType
		want string
	}{
		{"bulk", gousb.TransferTypeBulk, "bulk"},
		{"interrupt", gousb.TransferTypeInterrupt, "intr"},
		{"isochronous", gousb.TransferTypeIsochronous, "iso "},
		{"control", gousb.TransferTypeControl, "ctrl"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DescribeTransferType(tc.in); got != tc.want {
				t.Errorf("DescribeTransferType(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatEndpoint(t *testing.T) {
	tests := []struct {
		name string
		ep   EndpointInfo
		want string
	}{
		{
			"AT out bulk",
			EndpointInfo{Address: 0x03, Direction: gousb.EndpointDirectionOut, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
			"EP 0x03  OUT  bulk  maxPacket=512",
		},
		{
			"AT in bulk",
			EndpointInfo{Address: 0x84, Direction: gousb.EndpointDirectionIn, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
			"EP 0x84  IN   bulk  maxPacket=512",
		},
		{
			"QMI interrupt small packet",
			EndpointInfo{Address: 0x89, Direction: gousb.EndpointDirectionIn, TransferType: gousb.TransferTypeInterrupt, MaxPacketSize: 8},
			"EP 0x89  IN   intr  maxPacket=8",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatEndpoint(tc.ep); got != tc.want {
				t.Errorf("FormatEndpoint(%+v) = %q, want %q", tc.ep, got, tc.want)
			}
		})
	}
}

func TestFormatInterface(t *testing.T) {
	iface := InterfaceInfo{
		Number: 2, Class: 0xFF, SubClass: 0xFF, Protocol: 0xFF, Alternate: 0,
	}
	want := "Interface 2 (MI_02)  class=ff sub=ff proto=ff  alt=0"
	if got := FormatInterface(iface); got != want {
		t.Errorf("FormatInterface(%+v) = %q, want %q", iface, got, want)
	}
}

func TestSortEndpoints(t *testing.T) {
	eps := []EndpointInfo{
		{Address: 0x89},
		{Address: 0x03},
		{Address: 0x84},
		{Address: 0x05},
	}
	got := SortEndpoints(eps)
	// Verify ascending order.
	for i := 1; i < len(got); i++ {
		if got[i-1].Address >= got[i].Address {
			t.Errorf("not ascending at %d: %v", i, got)
		}
	}
	// Verify original slice is NOT mutated (defensive copy contract).
	if eps[0].Address != 0x89 {
		t.Errorf("SortEndpoints mutated input slice")
	}
}

func TestRender(t *testing.T) {
	ifaces := []InterfaceInfo{
		{
			Number: 2, Class: 0xFF, SubClass: 0xFF, Protocol: 0xFF, Alternate: 0,
			Endpoints: []EndpointInfo{
				{Address: 0x03, Direction: gousb.EndpointDirectionOut, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
				{Address: 0x84, Direction: gousb.EndpointDirectionIn, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
			},
		},
		{
			Number: 4, Class: 0xFF, SubClass: 0xFF, Protocol: 0xFF, Alternate: 0,
			Endpoints: []EndpointInfo{},
		},
	}
	out := Render(ifaces)
	// Spot-check key fragments rather than an exact full-string match — keeps
	// the test robust to cosmetic whitespace tweaks.
	mustContain := []string{
		"Interface 2 (MI_02)",
		"EP 0x03  OUT  bulk",
		"EP 0x84  IN   bulk",
		"Interface 4 (MI_04)",
		"(no endpoints)",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("Render output missing %q\nFull output:\n%s", s, out)
		}
	}
}

func TestFromConfig(t *testing.T) {
	// Build a ConfigDesc resembling the EC25 layout (unsorted, to verify the
	// sort contract). Endpoints are deliberately given out of address order.
	cfg := gousb.ConfigDesc{
		Number: 1,
		Interfaces: []gousb.InterfaceDesc{
			{
				Number: 4,
				AltSettings: []gousb.InterfaceSetting{{
					Number: 4, Alternate: 0,
					Class: 0xFF, SubClass: 0xFF, Protocol: 0xFF,
					Endpoints: map[gousb.EndpointAddress]gousb.EndpointDesc{
						0x89: {Direction: gousb.EndpointDirectionIn, TransferType: gousb.TransferTypeInterrupt, MaxPacketSize: 8},
						0x05: {Direction: gousb.EndpointDirectionOut, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
						0x88: {Direction: gousb.EndpointDirectionIn, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
					},
				}},
			},
			{
				Number: 2,
				AltSettings: []gousb.InterfaceSetting{{
					Number: 2, Alternate: 0,
					Class: 0xFF, SubClass: 0xFF, Protocol: 0xFF,
					Endpoints: map[gousb.EndpointAddress]gousb.EndpointDesc{
						0x84: {Direction: gousb.EndpointDirectionIn, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
						0x03: {Direction: gousb.EndpointDirectionOut, TransferType: gousb.TransferTypeBulk, MaxPacketSize: 512},
					},
				}},
			},
		},
	}

	got := FromConfig(cfg)
	if len(got) != 2 {
		t.Fatalf("FromConfig returned %d interfaces, want 2", len(got))
	}
	// Interfaces must be sorted ascending by number (input was 4, 2).
	if got[0].Number != 2 || got[1].Number != 4 {
		t.Errorf("interface order = [%d, %d], want [2, 4]", got[0].Number, got[1].Number)
	}
	// MI_02 endpoints sorted ascending: 0x03, 0x84.
	at := got[0]
	if len(at.Endpoints) != 2 || at.Endpoints[0].Address != 0x03 || at.Endpoints[1].Address != 0x84 {
		t.Errorf("MI_02 endpoints not sorted: %+v", at.Endpoints)
	}
	// MI_04 endpoints sorted ascending: 0x05, 0x88, 0x89.
	qmi := got[1]
	if len(qmi.Endpoints) != 3 {
		t.Fatalf("MI_04 endpoint count = %d, want 3", len(qmi.Endpoints))
	}
	wantAddrs := []uint8{0x05, 0x88, 0x89}
	for i, want := range wantAddrs {
		if qmi.Endpoints[i].Address != want {
			t.Errorf("MI_04 endpoint[%d].Address = 0x%02x, want 0x%02x", i, qmi.Endpoints[i].Address, want)
		}
	}
	// Spot-check class/protocol propagation.
	if at.Class != 0xFF || at.Alternate != 0 {
		t.Errorf("MI_02 class/alt mismatch: %+v", at)
	}
}

func TestFromConfigEmptyAltSettings(t *testing.T) {
	cfg := gousb.ConfigDesc{
		Interfaces: []gousb.InterfaceDesc{
			{Number: 0, AltSettings: nil}, // no alt settings
		},
	}
	got := FromConfig(cfg)
	if len(got) != 1 || got[0].Number != 0 || len(got[0].Endpoints) != 0 {
		t.Errorf("empty alt-settings handling wrong: %+v", got)
	}
}
