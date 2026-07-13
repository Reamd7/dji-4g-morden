// Command tun-helper 以 root 运行,创建 TUN 虚拟网卡 + QMI 拨号 + relay。
// 由 desktop app 通过 osascript sudo 启动(点「TUN 模式」时触发提权)。
// 写 PID 到 /tmp/tun-helper.pid 供 app 监控/停止。
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"golang.zx2c4.com/wireguard/tun"

	"dji-modem-research/internal/qmidatapath"
	"dji-modem-research/internal/qmitransport"
	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

func main() {
	apn := "3gnet"
	if len(os.Args) >= 3 && os.Args[1] == "-apn" {
		apn = os.Args[2]
	}

	// Write PID for app monitoring
	pid := os.Getpid()
	_ = os.WriteFile("/tmp/tun-helper.pid", []byte(fmt.Sprintf("%d", pid)), 0644)
	fmt.Printf("tun-helper PID=%d, APN=%s\n", pid, apn)

	ctx := context.Background()

	// 1. TUN device (macOS: utun, needs root)
	tunName := "utun"
	if runtime.GOOS != "darwin" {
		tunName = "qmi0"
	}
	tunDev, err := tun.CreateTUN(tunName, 1500)
	if err != nil {
		fatal("CreateTUN", err)
	}
	tunName, _ = tunDev.Name()
	fmt.Printf("TUN created: %s\n", tunName)

	// 2. QMI transport (MI_04 model B)
	transport, err := qmitransport.Open()
	if err != nil {
		fatal("QMI transport", err)
	}
	defer transport.Close()

	// 3. QMI client (SYNC)
	client, err := qmi.NewClientFromTransport(ctx, transport, qmi.DefaultClientOptions())
	if err != nil {
		fatal("QMI client", err)
	}
	defer client.Close()

	// 4. Manager with TUN interface (real route + DNS, not dummy)
	cfg := manager.Config{
		APN:        apn,
		EnableIPv4: true,
		EnableIPv6: true,
		Device:     manager.ModemDevice{NetInterface: tunName},
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	startCtx, startCancel := context.WithTimeout(ctx, 60*time.Second)
	if err := mgr.StartCoreContext(startCtx); err != nil {
		fatal("StartCore", err)
	}
	startCancel()
	time.Sleep(3 * time.Second)

	if err := mgr.Connect(); err != nil {
		fatal("Connect (WDS)", err)
	}

	// 5. Bulk endpoints + TUN relay bridge
	bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
	if err != nil {
		fatal("OpenBulkEndpoints", err)
	}

	offset := 0
	if runtime.GOOS == "darwin" {
		offset = 4
	}
	sink := qmidatapath.NewTUNPacketSink(tunDev, offset, 1500)
	bridge := qmidatapath.New(sink, bulkIn, bulkOut, 1500, true)
	if err := bridge.Start(ctx); err != nil {
		fatal("Bridge.Start", err)
	}

	// 6. DNS:设全局(Wi-Fi service)+ utun + flush cache。
	// 断 WiFi 后系统 DNS resolver 仍读 Wi-Fi service 的 DNS config(持久),
	// DNS query 到 4G DNS → 命中 0/1 route → utun → 4G。
	st := mgr.Settings()
	if st != nil && len(st.IPv4DNS1) > 0 {
		dns1 := st.IPv4DNS1.String()
		dns2 := ""
		if len(st.IPv4DNS2) > 0 {
			dns2 = st.IPv4DNS2.String()
		}
		configureDNS(tunName, dns1, dns2)
	}

	ipStr := ""
	if st != nil && len(st.IPv4Address) > 0 {
		ipStr = st.IPv4Address.String()
	}
	fmt.Printf("TUN %s active: IP=%s, relay running. Press Ctrl+C to stop.\n", tunName, ipStr)

	// Write status for app to read
	_ = os.WriteFile("/tmp/tun-helper.status", []byte(fmt.Sprintf(
		`{"tun":"%s","ip":"%s","running":true}`, tunName, ipStr,
	)), 0644)

	// Wait for SIGINT/SIGTERM (app sends kill)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	// Cleanup
	restoreDNS()
	bridge.Stop()
	tunDev.Close()
	mgr.Stop()
	_ = os.Remove("/tmp/tun-helper.pid")
	_ = os.Remove("/tmp/tun-helper.status")
	fmt.Println("tun-helper stopped.")
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "tun-helper FATAL: %s: %v\n", msg, err)
	_ = os.Remove("/tmp/tun-helper.pid")
	os.Exit(1)
}

// configureDNS 设 utun + Wi-Fi(全局)DNS + flush cache。
// Wi-Fi DNS 即使断 WiFi(inactive),config 仍保留,系统 resolver 仍读。
func configureDNS(ifname, dns1, dns2 string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// utun 接口 DNS
	_ = exec.Command("networksetup", "-setdnsservers", ifname, dns1, dns2).Run()
	// Wi-Fi service DNS(全局,断 WiFi 后仍保留 config)
	_ = exec.Command("networksetup", "-setdnsservers", "Wi-Fi", dns1, dns2).Run()
	// flush DNS cache(强制 resolver 用新 DNS)
	_ = exec.Command("dscacheutil", "-flushcache").Run()
	_ = exec.Command("killall", "-HUP", "mDNSResponder").Run()
	fmt.Printf("DNS configured: %s %s (utun + Wi-Fi)\n", dns1, dns2)
}

// restoreDNS 还原 Wi-Fi DNS(clear = DHCP 自动)+ flush。
func restoreDNS() {
	if runtime.GOOS != "darwin" {
		return
	}
	_ = exec.Command("networksetup", "-setdnsservers", "Wi-Fi", "empty").Run()
	_ = exec.Command("dscacheutil", "-flushcache").Run()
	_ = exec.Command("killall", "-HUP", "mDNSResponder").Run()
	fmt.Println("DNS restored to DHCP.")
}
