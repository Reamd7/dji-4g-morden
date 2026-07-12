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
	"os/signal"
	"runtime"
	"time"

	"net/netip"

	"golang.zx2c4.com/wireguard/tun"

	"dji-modem-research/internal/qmidatapath"
	"dji-modem-research/internal/qmitransport"
	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

func main() {
	dial := flag.Bool("dial", false, "perform WDS dialup (activates PDP context, may incur data charges)")
	tunMode := flag.Bool("tun", false, "create TUN + start relay for actual internet (implies -dial, needs admin)")
	socks5Mode := flag.Bool("socks5", false, "start SOCKS5 proxy via netstack (implies -dial, no admin needed)")
	socks5Addr := flag.String("socks5-addr", "127.0.0.1:1080", "SOCKS5 listen address")
	apn := flag.String("apn", "3gnet", "APN for dialup")
	flag.Parse()

	if *tunMode {
		*dial = true // -tun implies -dial
	}
	if *socks5Mode {
		*dial = true // -socks5 implies -dial
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
	} else if *socks5Mode {
		fmt.Println("[3/8] SOCKS5 mode: no TUN needed (netstack userspace TCP/IP)")
		tunName = "dummy" // triggers WDA allocation; NoRoute+NoDNS skip OS network config
	} else {
	}

	// 4. Manager (USB injection via NewWithClient)
	fmt.Println("[4/8] Creating manager (NewWithClient, hook bypasses /dev/cdc-wdm0)...")
	cfg := manager.Config{
		APN:        *apn,
		EnableIPv4: true,
		EnableIPv6: true,
		Device:     manager.ModemDevice{NetInterface: tunName},
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
	}
	if *socks5Mode {
		cfg.NoRoute = true
		cfg.NoDNS = true
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
		tunSink := qmidatapath.NewTUNPacketSink(tunDev, offset, 1500)
		// zlp=true: subplan 00 D2 confirmed QDC507 needs ZLP for 512-multiple packets
		bridge = qmidatapath.New(tunSink, bulkIn, bulkOut, 1500, true)
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
		allowICMPFirewall()

		tunIP := ""
		if s := mgr.Settings(); s != nil && len(s.IPv4Address) > 0 {
			tunIP = s.IPv4Address.String()
		}

		time.Sleep(2 * time.Second) // let relay stabilize

		// Diagnostics: confirm the default route points at the TUN and show DNS
		// resolver state. The manager's logrus logs aren't surfaced here, so a
		// silent route-add failure (or a route that doesn't actually carry
		// traffic) would otherwise be invisible.
		fmt.Println("\n── Routing/interface diagnostics ──")
		runCommand("netstat", "-rn", "-f", "inet")
		if runtime.GOOS == "darwin" {
			runCommand("ifconfig", tunName)
			runCommand("scutil", "--dns")
		} else if runtime.GOOS == "windows" {
			runCommand("ipconfig", tunName)
		}
		fmt.Println("── end diagnostics ──")

		// ════════════════════════════════════════════════════════════════
		// Phase 1: High-metric route — source-bound (-S) goes through 4G,
		//           default traffic stays on main network (lower metric wins)
		// ════════════════════════════════════════════════════════════════
		fmt.Println("\n════ Phase 1: Source-bound (bind to TUN IP) ══════════")
		setInterfaceMetric(tunName, 100)

		fmt.Println("  [1a] Main network — ping baidu.com:")
		runCommand("ping", platformPingArgs("baidu.com")...)
		if tunIP != "" {
			if srcArgs := platformPingArgsWithSource("baidu.com", tunIP); srcArgs != nil {
				fmt.Printf("  [1b] 4G TUN — ping -S %s baidu.com:\n", tunIP)
				runCommand("ping", srcArgs...)
			} else {
				fmt.Printf("  [1b] 4G TUN — ping source-bound: skipped (macOS ping -I takes an iface name, not an IP; curl --interface covers it)\n")
			}
			fmt.Println("  [1c] 4G TUN — curl --interface:")
			runCommand("curl", "-s", "-o", nullDevice(), "-w", "%{http_code} %{time_total}s",
				"--interface", tunIP, "http://www.baidu.com")
			fmt.Println()
		}

		txPkt1, _, rxPkt1, _ := bridge.Stats()

		// ════════════════════════════════════════════════════════════════
		// Phase 2: Global TUN — metric=1 (ALL traffic → 4G)
		// ════════════════════════════════════════════════════════════════
		fmt.Println("\n════ Phase 2: Global TUN (all traffic → 4G) ═══════════")
		setInterfaceMetric(tunName, 1)

		fmt.Println("  [2a] ping baidu.com (all through 4G):")
		runCommand("ping", platformPingArgs("baidu.com")...)
		fmt.Println("  [2b] curl http://www.baidu.com:")
		runCommand("curl", "-s", "-o", nullDevice(), "-w", "%{http_code} %{time_total}s", "http://www.baidu.com")
		fmt.Println()
		fmt.Println("  [2c] nslookup baidu.com:")
		runCommand("nslookup", "baidu.com")

		setInterfaceMetric(tunName, -1) // reset to auto (Windows only)

		txPkt2, txByt2, rxPkt2, rxByt2 := bridge.Stats()
		fmt.Printf("\n  Relay stats: TX %d pkts/%d B, RX %d pkts/%d B\n", txPkt2, txByt2, rxPkt2, rxByt2)
		fmt.Printf("  Phase 1: %d TX + %d RX (source-bound)\n", txPkt1, rxPkt1)
		fmt.Printf("  Phase 2: %d TX + %d RX (global TUN)\n", txPkt2-txPkt1, rxPkt2-rxPkt1)

		fmt.Println("\n  Tests complete. Disconnecting...")
	} else if *socks5Mode {
		// SOCKS5 mode: create netstack sink + relay + SOCKS5 server
		fmt.Println("[8/8] Starting SOCKS5 relay (bulk EP ↔ netstack)...")

		bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
		if err != nil {
			fmt.Fprintf(os.Stderr, "OpenBulkEndpoints failed: %v\n", err)
			mgr.Stop()
			client.Close()
			transport.Close()
			os.Exit(1)
		}
		fmt.Println("      OK — bulk IN 0x88 + bulk OUT 0x05 opened")

		s := mgr.Settings()
		if s == nil || len(s.IPv4Address) == 0 {
			fmt.Fprintf(os.Stderr, "No IPv4 address from dialup\n")
			mgr.Stop()
			client.Close()
			transport.Close()
			os.Exit(1)
		}

		// Create netstack sink with modem-assigned IP
		localIP := netip.AddrFrom4([4]byte{s.IPv4Address[0], s.IPv4Address[1], s.IPv4Address[2], s.IPv4Address[3]})
		netstackSink, err := qmidatapath.NewNetstackPacketSink(localIP, int(s.MTU), true, netip.Addr{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "NewNetstackPacketSink failed: %v\n", err)
			mgr.Stop()
			client.Close()
			transport.Close()
			os.Exit(1)
		}

		bridge = qmidatapath.New(netstackSink, bulkIn, bulkOut, int(s.MTU), true)
		if err := bridge.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Bridge.Start failed: %v\n", err)
			netstackSink.Close()
			mgr.Stop()
			client.Close()
			transport.Close()
			os.Exit(1)
		}

		// Start SOCKS5 server
		socksCtx, socksCancel := context.WithCancel(ctx)
		go func() {
			fmt.Printf("      SOCKS5 listening on %s (no admin needed)\n", *socks5Addr)
			fmt.Printf("      curl --socks5-hostname %s http://www.baidu.com\n", *socks5Addr)
			if err := qmidatapath.RunSOCKS5(socksCtx, netstackSink, *socks5Addr); err != nil {
				fmt.Fprintf(os.Stderr, "SOCKS5 server: %v\n", err)
			}
		}()

		// Wait for Ctrl+C
		fmt.Println("\n  Press Ctrl+C to stop...")
		waitForSignal()

		// Cleanup socks5
		socksCancel()
		netstackSink.Close()
		bridge.Stop()
		mgr.Stop()
		client.Close()
		transport.Close()
		fmt.Println("Done.")
		return
	} else {
		// Non-TUN mode: hold for 5s then exit (stage 2 behavior)
		fmt.Println("\nHolding connection for 5s to verify stability...")
		time.Sleep(5 * time.Second)
	}

	// Cleanup (order matters: tun.Close → bridge.Stop → mgr.Stop → transport.Close)
	fmt.Println("\nDisconnecting...")
	fmt.Println("  Restoring DNS to pre-4G values...")
	restoreDNS()
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
	if runtime.GOOS == "darwin" {
		// macOS ping -I takes an interface NAME, not a source IP, and rejects
		// it for unicast destinations ("flags cannot be used with unicast
		// destination"). Source-bound ping isn't supported on macOS; return
		// nil so the caller skips it (curl --interface already covers this).
		return nil
	}
	return []string{"-c", "4", "-I", srcIP, host}
}

// nullDevice returns the platform null-output path for curl -o etc.
// NUL on Windows, /dev/null on Unix.
func nullDevice() string {
	if runtime.GOOS == "windows" {
		return "NUL"
	}
	return "/dev/null"
}

// setInterfaceMetric sets the interface route metric on Windows (netsh).
// No-op on macOS/Linux: the manager's netcfg (DarwinConfigurator) already
// added the default route via AddDefaultRouteDirect, so traffic is routed
// to the TUN without needing a metric override. metric < 0 means "reset auto".
func setInterfaceMetric(ifname string, metric int) {
	if runtime.GOOS != "windows" {
		return
	}
	m := fmt.Sprintf("metric=%d", metric)
	if metric < 0 {
		m = "metric=auto"
	}
	runCommand("netsh", "interface", "ipv4", "set", "interface", ifname, m)
}

// allowICMPFirewall adds ICMP allow rules on Windows Firewall; no-op elsewhere.
func allowICMPFirewall() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Println("      Adding Windows Firewall ICMP rules...")
	runCommand("netsh", "advfirewall", "firewall", "add", "rule",
		"name=qmi-tun-icmp-out", "protocol=icmpv4:8,any", "dir=out", "action=allow")
	runCommand("netsh", "advfirewall", "firewall", "add", "rule",
		"name=qmi-tun-icmp-in", "protocol=icmpv4:0,any", "dir=in", "action=allow")
}

// waitForSignal blocks until SIGINT (Ctrl+C) or SIGTERM.
func waitForSignal() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	<-sigCh
}
