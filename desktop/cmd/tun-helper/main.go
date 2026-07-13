// Command tun-helper 以 root 运行,创建 TUN 虚拟网卡 + QMI 拨号 + relay。
// 由 desktop app 通过 osascript sudo 启动(点「TUN 模式」时触发提权)。
// 写 PID 到 /tmp/tun-helper.pid 供 app 监控/停止。
package main

import (
	"context"
	"fmt"
	"net"
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

	// 6. DNS:启动本地 DNS proxy(127.0.0.1:53 → 4G DNS via utun11),
	// 设系统 DNS = 127.0.0.1(lo0 永远 Reachable,绕过 configd Not Reachable)。
	st := mgr.Settings()
	if st != nil && len(st.IPv4DNS1) > 0 {
		dns1 := st.IPv4DNS1.String()
		// 本地 DNS proxy:转发到 4G DNS(经 utun11)
		go runDNSProxy(ctx, dns1)
		// 设系统 DNS = 127.0.0.1 + flush
		configureDNS(tunName, dns1, "")
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

// configureDNS 设系统 DNS = 127.0.0.1(本地 proxy)+ /etc/resolver/default + flush。
func configureDNS(ifname, dns1, dns2 string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// 系统 DNS = 127.0.0.1(lo0 永远 Reachable,绕过 configd 的 Wi-Fi Not Reachable)
	_ = exec.Command("networksetup", "-setdnsservers", "Wi-Fi", "127.0.0.1").Run()
	// /etc/resolver/default 作为 fallback
	_ = os.MkdirAll("/etc/resolver", 0755)
	_ = os.WriteFile("/etc/resolver/default", []byte("nameserver 127.0.0.1\n"), 0644)
	// flush
	_ = exec.Command("dscacheutil", "-flushcache").Run()
	_ = exec.Command("killall", "-HUP", "mDNSResponder").Run()
	fmt.Printf("DNS proxy on 127.0.0.1:53 → %s, system DNS = 127.0.0.1\n", dns1)
}

// restoreDNS 还原:删 /etc/resolver/default + 清 Wi-Fi DNS + flush。
func restoreDNS() {
	if runtime.GOOS != "darwin" {
		return
	}
	_ = os.Remove("/etc/resolver/default")
	_ = exec.Command("networksetup", "-setdnsservers", "Wi-Fi", "empty").Run()
	_ = exec.Command("dscacheutil", "-flushcache").Run()
	_ = exec.Command("killall", "-HUP", "mDNSResponder").Run()
	fmt.Println("DNS restored.")
}

// runDNSProxy 监听 127.0.0.1:53,转发 UDP DNS query 到 upstream(4G DNS)。
// upstream 的 DNS query 经 utun11(0/1 route)→ USB → modem → 4G。
func runDNSProxy(ctx context.Context, upstream string) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}, Port: 53})
	if err != nil {
		fmt.Fprintf(os.Stderr, "DNS proxy: listen 127.0.0.1:53: %v\n", err)
		return
	}
	defer conn.Close()
	fmt.Printf("DNS proxy listening on 127.0.0.1:53 → %s\n", upstream)

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 1500)
	for {
		n, client, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		query := make([]byte, n)
		copy(query, buf[:n])

		go func(q []byte, c *net.UDPAddr) {
			uc, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(upstream), Port: 53})
			if err != nil {
				return
			}
			defer uc.Close()
			uc.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := uc.Write(q); err != nil {
				return
			}
			resp := make([]byte, 1500)
			rn, err := uc.Read(resp)
			if err != nil {
				return
			}
			conn.WriteToUDP(resp[:rn], c)
		}(query, client)
	}
}
