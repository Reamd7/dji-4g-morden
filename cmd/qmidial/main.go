// Command qmidial performs QMI dialup via USB transport (model B) and the
// quectel-qmi-go manager. It exercises the full chain:
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
//
//	# Stage 3: dialup + TUN relay for actual internet (needs admin + wintun.dll)
//	mise exec -- go build -o qmidial.exe ./cmd/qmidial
//	./qmidial.exe -dial -tun
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.zx2c4.com/wireguard/tun"

	"dji-modem-research/internal/qmidatapath"
	"dji-modem-research/internal/qmitransport"
	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/netcfg"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

func main() {
	dial := flag.Bool("dial", false, "perform WDS dialup (activates PDP context, may incur data charges)")
	tunMode := flag.Bool("tun", false, "create TUN + start relay for actual internet (implies -dial, needs admin)")
	apn := flag.String("apn", "3gnet", "APN for dialup")
	flag.Parse()

	if *tunMode {
		*dial = true // -tun implies -dial
	}

	ctx := context.Background()

	// 1. USB transport (model B: EP0 control encapsulation + DTR)
	fmt.Println("[1/8] Opening QMITransport (MI_04, model B + DTR)...")
	transport, err := qmitransport.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Open transport failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("      OK — MI_04 claimed, DTR set, interrupt goroutine running")

	// 2. QMI client (SyncOnOpen sends CTL SYNC internally)
	fmt.Println("[2/8] Creating QMI client (NewClientFromTransport + SyncOnOpen)...")
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
		transport.Close()
		fmt.Fprintf(os.Stderr, "NewClientFromTransport failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("      OK — SYNC exchanged, readLoop/writerLoop/indicationLoop running")

	// 3. TUN device (before manager, so configureNetwork can set IP on it)
	var tunDev tun.Device
	var tunName string
	if *tunMode {
		fmt.Println("[3/8] Creating TUN device...")
		// Platform-specific naming: macOS must be "utun", Windows/Linux use custom name
		tunName = "qmi0"
		if runtime.GOOS == "darwin" {
			tunName = "utun"
		}
		// Pre-load wintun.dll using full path (bypass LOAD_LIBRARY_SEARCH_APPLICATION_DIR resolution issues)
		if err := preloadWintunDLL(); err != nil {
			fmt.Printf("      warn: preloadWintunDLL: %v (continuing)\n", err)
		}
		tunDev, err = tun.CreateTUN(tunName, 1500)
		if err != nil {
			client.Close()
			transport.Close()
			fmt.Fprintf(os.Stderr, "CreateTUN failed: %v\n", err)
			os.Exit(1)
		}
		tunName, _ = tunDev.Name() // actual name (macOS may rename to utunN)
		fmt.Printf("      OK — TUN created: %s (MTU 1500)\n", tunName)
	} else {
		fmt.Println("[3/8] Skipping TUN (no -tun flag)")
	}

	// 4. Manager (USB injection via NewWithClient)
	fmt.Println("[4/8] Creating manager (NewWithClient, hook bypasses /dev/cdc-wdm0)...")
	cfg := manager.Config{
		APN:        *apn,
		EnableIPv4: true,
		EnableIPv6: true,
		Device:     manager.ModemDevice{NetInterface: tunName}, // triggers WDA allocation when non-empty
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)
	fmt.Println("      OK — hook set, client injected")

	// 5. Start core (allocate CTL/WDA/WDS/NAS/DMS/UIM services)
	fmt.Println("[5/8] Starting manager core (service allocation)...")
	startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
	defer startCancel()
	if err := mgr.StartCoreContext(startCtx); err != nil {
		fmt.Fprintf(os.Stderr, "StartCore failed: %v\n", err)
		if tunDev != nil {
			tunDev.Close()
		}
		client.Close()
		transport.Close()
		os.Exit(1)
	}
	fmt.Println("      OK — QMI services allocated (WDA enables raw-IP mode)")

	// Wait for async PreWarmIdentities to populate the snapshot.
	time.Sleep(3 * time.Second)

	// 6. Device info (read-only — safe for SIM)
	fmt.Println("[6/8] Querying device info (read-only)...")
	printDeviceInfo(mgr)

	if !*dial {
		fmt.Println("\n[7/8] Skipping dialup (use -dial to activate PDP context)")
		mgr.Stop()
		if tunDev != nil {
			tunDev.Close()
		}
		client.Close()
		transport.Close()
		return
	}

	// 7. Dialup (WDS StartNetwork — activates data connection)
	fmt.Printf("[7/8] Dialing (WDS StartNetwork, APN=%s, IPv4+IPv6)...\n", *apn)
	if err := mgr.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Connect failed: %v\n", err)
		mgr.Stop()
		if tunDev != nil {
			tunDev.Close()
		}
		client.Close()
		transport.Close()
		os.Exit(1)
	}
	fmt.Println("      OK — data call active, network configured on TUN")
	printConnectionInfo(mgr)

	// 8. TUN relay (if -tun)
	var bridge *qmidatapath.Bridge
	if *tunMode {
		fmt.Println("[8/8] Starting TUN relay (bulk EP ↔ TUN)...")

		bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
		if err != nil {
			fmt.Fprintf(os.Stderr, "OpenBulkEndpoints failed: %v\n", err)
			mgr.Stop()
			tunDev.Close()
			client.Close()
			transport.Close()
			os.Exit(1)
		}
		fmt.Println("      OK — bulk IN 0x88 + bulk OUT 0x05 opened")

		offset := 0
		if runtime.GOOS == "darwin" {
			offset = 4
		}
		// zlp=true: subplan 00 D2 confirmed QDC507 needs ZLP for 512-multiple packets
		bridge = qmidatapath.New(tunDev, bulkIn, bulkOut, offset, 1500, true)
		if err := bridge.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Bridge.Start failed: %v\n", err)
			mgr.Stop()
			tunDev.Close()
			client.Close()
			transport.Close()
			os.Exit(1)
		}
		fmt.Printf("      OK — relay active: TUN %s ↔ bulk EP (zlp=true)\n", tunName)

		// Configure DNS (netcfg.UpdateDNS is broken on Windows/macOS)
		s := mgr.Settings()
		if s != nil && len(s.IPv4DNS1) > 0 {
			fmt.Printf("      Configuring DNS: %s, %s\n", s.IPv4DNS1, s.IPv4DNS2)
			if err := configureDNS(tunName, s.IPv4DNS1.String(), s.IPv4DNS2.String()); err != nil {
				fmt.Printf("      warn: DNS config failed: %v\n", err)
			} else {
				fmt.Println("      OK — DNS configured")
			}
		}
		// Fix routing: TUN is point-to-point, needs direct route (not gateway)
		fmt.Printf("      Adding direct default route via %s...\n", tunName)
		if err := netcfg.AddDefaultRouteDirect(tunName, false); err != nil {
			fmt.Printf("      warn: AddDefaultRouteDirect: %v\n", err)
		} else {
			fmt.Println("      OK — default route added (direct, no gateway)")
		}

		// Verify connectivity
		time.Sleep(2 * time.Second) // let relay stabilize

		// Allow ICMP through Windows Firewall (Wintun adapter is "public" by default)
		fmt.Println("  Adding Windows Firewall ICMP allow rule...")
		runCommand("netsh", "advfirewall", "firewall", "add", "rule",
			"name=qmi-tun-icmp", "protocol=icmpv4:8,any", "dir=out", "action=allow")

		fmt.Println("  Testing ping 114.114.114.114 through TUN...")
		runCommand("ping", platformPingArgs("114.114.114.114")...)
		// Also try source-bound ping (force through TUN IP)
		if s := mgr.Settings(); s != nil && len(s.IPv4Address) > 0 {
			fmt.Printf("  Testing ping -S %s 114.114.114.114...\n", s.IPv4Address)
			runCommand("ping", platformPingArgsWithSource("114.114.114.114", s.IPv4Address.String())...)
		}
		fmt.Println("  Testing DNS resolution (nslookup baidu.com)...")
		runCommand("nslookup", "baidu.com")
		fmt.Println("  Testing TCP (curl http://www.baidu.com)...")
		runCommand("curl", "-s", "-o", "/dev/null", "-w", "%{http_code} %{time_total}s", "http://www.baidu.com")

		// Tests done — auto-exit (cleanup follows)
		fmt.Println("\n  Tests complete. Disconnecting...")
	} else {
		// Non-TUN mode: hold for 5s then exit (stage 2 behavior)
		fmt.Println("\nHolding connection for 5s to verify stability...")
		time.Sleep(5 * time.Second)
	}

	// Cleanup (order matters: tun.Close → bridge.Stop → mgr.Stop → transport.Close)
	fmt.Println("\nDisconnecting...")
	if bridge != nil {
		bridge.Stop()
	}
	if tunDev != nil {
		tunDev.Close()
	}
	mgr.Stop()
	client.Close()
	transport.Close()
	fmt.Println("Done.")
}

func printDeviceInfo(mgr *manager.Manager) {
	snap := mgr.GetDeviceSnapshot()
	if snap == nil {
		return
	}
	ids, ok := snap.Identities()
	if !ok {
		return
	}
	if ids.IMSI != "" {
		fmt.Printf("  IMSI:    %s\n", ids.IMSI)
	}
	if ids.IMEI != "" {
		fmt.Printf("  IMEI:    %s\n", ids.IMEI)
	}
	if ids.ICCID != "" {
		fmt.Printf("  ICCID:   %s\n", ids.ICCID)
	}
	if ids.Model != "" {
		fmt.Printf("  Model:   %s\n", ids.Model)
	}
	if ids.Manufacturer != "" {
		fmt.Printf("  Mfr:     %s\n", ids.Manufacturer)
	}
	if ids.FirmwareRevision != "" {
		fmt.Printf("  FW:      %s\n", ids.FirmwareRevision)
	}
}

func printConnectionInfo(mgr *manager.Manager) {
	s := mgr.Settings()
	if s == nil {
		fmt.Println("  (no settings available)")
		return
	}
	if s.IPv4Address != nil {
		fmt.Printf("  IPv4:    %s/%s\n", s.IPv4Address, s.IPv4Subnet)
	}
	if s.IPv4Gateway != nil {
		fmt.Printf("  GW:      %s\n", s.IPv4Gateway)
	}
	if len(s.IPv4DNS1) > 0 {
		fmt.Printf("  DNS:     %s, %s\n", s.IPv4DNS1, s.IPv4DNS2)
	}
	fmt.Printf("  MTU:     %d\n", s.MTU)
	fmt.Printf("  PDH:     0x%08x\n", mgr.HandleV4())

	// IPv6
	if h6 := mgr.HandleV6(); h6 != 0 {
		s6 := mgr.SettingsV6()
		if s6 != nil && len(s6.IPv6Address) > 0 {
			fmt.Printf("  IPv6:    %s/%d\n", s6.IPv6Address, s6.IPv6Prefix)
		}
	}
}

// runCommand runs a command and pipes its output to stdout.
func runCommand(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  (command failed: %v)\n", err)
	}
}

// platformPingArgs returns platform-specific ping arguments.
func platformPingArgs(host string) []string {
	if runtime.GOOS == "windows" {
		return []string{"-n", "4", host}
	}
	return []string{"-c", "4", host}
}

// platformPingArgsWithSource returns ping args bound to a source IP.
func platformPingArgsWithSource(host, srcIP string) []string {
	if runtime.GOOS == "windows" {
		return []string{"-n", "4", "-S", srcIP, host}
	}
	return []string{"-c", "4", "-I", srcIP, host}
}
