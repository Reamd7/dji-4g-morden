// Command qmidial performs QMI dialup via USB transport (model B) and the
// quectel-qmi-go manager. It exercises the full stage 2 chain:
//
//	QMITransport → qmi.NewClientFromTransport → manager.NewWithClient → Start → Connect
//
// Usage:
//
//	# Read-only: open transport + start manager (service allocation, no dialing)
//	mise exec -- go run ./cmd/qmidial
//
//	# Full dialup: also Connect (WDS StartNetwork, activates PDP context)
//	mise exec -- go run ./cmd/qmidial -dial
//
//	# Custom APN (default: 3gnet for the carrier)
//	mise exec -- go run ./cmd/qmidial -dial -apn wonet
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"dji-modem-research/internal/qmitransport"
	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

func main() {
	dial := flag.Bool("dial", false, "perform WDS dialup (activates PDP context, may incur data charges)")
	apn := flag.String("apn", "3gnet", "APN for dialup")
	flag.Parse()

	ctx := context.Background()

	// 1. USB transport (model B: EP0 control encapsulation + DTR)
	fmt.Println("[1/6] Opening QMITransport (MI_04, model B + DTR)...")
	transport, err := qmitransport.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Open transport failed: %v\n", err)
		os.Exit(1)
	}
	defer transport.Close()
	fmt.Println("      OK — MI_04 claimed, DTR set, interrupt goroutine running")

	// 2. QMI client (SyncOnOpen sends CTL SYNC internally)
	fmt.Println("[2/6] Creating QMI client (NewClientFromTransport + SyncOnOpen)...")
	clientOpts := qmi.DefaultClientOptions()
	client, err := qmi.NewClientFromTransport(ctx, transport, clientOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewClientFromTransport failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("      OK — SYNC exchanged, readLoop/writerLoop/indicationLoop running")

	// 3. Manager (USB injection via NewWithClient)
	fmt.Println("[3/6] Creating manager (NewWithClient, hook bypasses /dev/cdc-wdm0)...")
	cfg := manager.Config{
		APN:        *apn,
		EnableIPv4: true,
	}
	mgr := manager.NewWithClient(cfg, nil, client)
	fmt.Println("      OK — hook set, client injected")

	// 4. Start core (allocate CTL/WDA/WDS/NAS/DMS/UIM services)
	fmt.Println("[4/6] Starting manager core (service allocation)...")
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	defer startCancel()
	if err := mgr.StartCoreContext(startCtx); err != nil {
		fmt.Fprintf(os.Stderr, "StartCore failed: %v\n", err)
		client.Close()
		os.Exit(1)
	}
	defer mgr.Stop()
	fmt.Println("      OK — all QMI services allocated (NAS/DMS/UIM/WDA/WDS)")

	// 5. Device info (read-only — safe for SIM)
	fmt.Println("[5/6] Querying device info (read-only)...")
	printDeviceInfo(mgr)

	if !*dial {
		fmt.Println("\n[6/6] Skipping dialup (use -dial to activate PDP context)")
		return
	}

	// 6. Dialup (WDS StartNetwork — activates data connection)
	fmt.Printf("[6/6] Dialing (WDS StartNetwork, APN=%s)...\n", *apn)
	if err := mgr.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Connect failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("      OK — data call active")

	// Print IP details
	printConnectionInfo(mgr)

	// Hold the connection for a few seconds to verify stability
	fmt.Println("\nHolding connection for 5s to verify stability...")
	time.Sleep(5 * time.Second)
	fmt.Println("Done. Disconnecting...")
}

func printDeviceInfo(mgr *manager.Manager) {
	snap := mgr.GetDeviceSnapshot()

	if ids, ok := snap.Identities(); ok {
		fmt.Printf("      Model:    %s\n", ids.Model)
		fmt.Printf("      Firmware: %s\n", ids.FirmwareRevision)
		fmt.Printf("      IMEI:     %s\n", ids.IMEI)
		fmt.Printf("      IMSI:     %s\n", ids.IMSI)
		fmt.Printf("      ICCID:    %s\n", ids.ICCID)
	}

	if sig, _ := snap.Signal(); sig != nil {
		fmt.Printf("      Signal:   %d dBm (RSRP %d)\n", sig.RSSI, sig.RSRP)
	}

	if opName, _, ok := snap.NASOperatorName(); ok && opName != nil {
		fmt.Printf("      Operator: %s\n", opName.ServiceProviderName)
	}
}

func printConnectionInfo(mgr *manager.Manager) {
	s := mgr.Settings()
	if s == nil {
		fmt.Println("      (no runtime settings available)")
		return
	}
	if len(s.IPv4Address) > 0 {
		ones, _ := s.IPv4Subnet.Size()
		fmt.Printf("      IPv4:     %s/%d\n", s.IPv4Address, ones)
	}
	if len(s.IPv4Gateway) > 0 {
		fmt.Printf("      Gateway:  %s\n", s.IPv4Gateway)
	}
	if len(s.IPv4DNS1) > 0 {
		fmt.Printf("      DNS:      %s", s.IPv4DNS1)
		if len(s.IPv4DNS2) > 0 {
			fmt.Printf(", %s", s.IPv4DNS2)
		}
		fmt.Println()
	}
	if s.MTU > 0 {
		fmt.Printf("      MTU:      %d\n", s.MTU)
	}
}
