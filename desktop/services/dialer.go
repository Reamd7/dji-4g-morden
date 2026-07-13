// DialerService 提供拨号上网(QMI WDS)+ SOCKS5 代理(netstack)能力。
// 独立于 DeviceService:用 MI_04 QMI 接口(AT 用 MI_02,两者不冲突)。
package services

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"dji-modem-research/internal/qmidatapath"
	"dji-modem-research/internal/qmitransport"
	"dji-modem-research/third_party/quectel-qmi-go/manager"
	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

// ConnectionInfo 是拨号后的网络配置(前端展示用)。
type ConnectionInfo struct {
	IPv4Address string `json:"ipv4Address"`
	Gateway     string `json:"gateway"`
	DNS1        string `json:"dns1"`
	DNS2        string `json:"dns2"`
	MTU         int    `json:"mtu"`
	IPv6Address string `json:"ipv6Address"`
}

// RelayStats 是 SOCKS5 relay 的流量统计。
type RelayStats struct {
	TXPackets int64 `json:"txPackets"`
	TXBytes   int64 `json:"txBytes"`
	RXPackets int64 `json:"rxPackets"`
	RXBytes   int64 `json:"rxBytes"`
}

// DialerService 管理 QMI 拨号 + SOCKS5 代理的完整生命周期。
type DialerService struct {
	mu sync.Mutex

	transport *qmitransport.QMITransport
	client    *qmi.Client
	manager   *manager.Manager

	ctx    context.Context
	cancel context.CancelFunc

	bridge      *qmidatapath.Bridge
	sink        *qmidatapath.NetstackPacketSink
	socksCancel context.CancelFunc
	tunPID      int // TUN helper 进程 PID(0=未运行)
}

func (s *DialerService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	return nil
}

// Dial 执行 QMI 拨号:Open MI_04 → SYNC → StartCore → Connect(WDS StartNetwork)。
// 拿到运营商 IP/DNS/MTU。apn 为空时默认 3gnet。
func (s *DialerService) Dial(apn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manager != nil {
		return fmt.Errorf("已拨号,请先断开")
	}
	if apn == "" {
		apn = "3gnet"
	}

	transport, err := qmitransport.Open()
	if err != nil {
		return fmt.Errorf("打开 QMI 通道(MI_04): %w", err)
	}

	client, err := qmi.NewClientFromTransport(context.Background(), transport, qmi.DefaultClientOptions())
	if err != nil {
		transport.Close()
		return fmt.Errorf("QMI client: %w", err)
	}

	cfg := manager.Config{
		APN:        apn,
		EnableIPv4: true,
		EnableIPv6: true,
		Device:     manager.ModemDevice{NetInterface: "dummy"},
		Timeouts: manager.TimeoutConfig{
			IndicationRegister: 15 * time.Second,
			Init:               30 * time.Second,
		},
		NoRoute: true,
		NoDNS:   true,
	}
	mgr := manager.NewWithClient(cfg, nil, client)

	startCtx, startCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer startCancel()
	if err := mgr.StartCoreContext(startCtx); err != nil {
		client.Close()
		transport.Close()
		return fmt.Errorf("StartCore: %w", err)
	}
	time.Sleep(3 * time.Second)

	if err := mgr.Connect(); err != nil {
		mgr.Stop()
		client.Close()
		transport.Close()
		return fmt.Errorf("拨号失败: %w", err)
	}

	s.transport = transport
	s.client = client
	s.manager = mgr
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return nil
}

// Hangup 断开拨号(含 SOCKS5 自动停止)+ 释放 transport/client/manager。
func (s *DialerService) Hangup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manager == nil {
		return nil
	}
	s.stopSOCKS5Locked()
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.manager.Stop()
	s.client.Close()
	s.transport.Close()
	s.manager = nil
	s.client = nil
	s.transport = nil
	s.ctx = nil
	s.cancel = nil
	return nil
}

// IsConnected 返回是否已拨号。
func (s *DialerService) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manager != nil
}

// GetConnection 返回拨号后的网络配置(IP/Gateway/DNS/MTU/IPv6)。
func (s *DialerService) GetConnection() (*ConnectionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manager == nil {
		return nil, fmt.Errorf("未拨号")
	}
	info := &ConnectionInfo{}
	if st := s.manager.Settings(); st != nil {
		if st.IPv4Address != nil {
			info.IPv4Address = st.IPv4Address.String()
		}
		if st.IPv4Gateway != nil {
			info.Gateway = st.IPv4Gateway.String()
		}
		if st.IPv4DNS1 != nil {
			info.DNS1 = st.IPv4DNS1.String()
		}
		if st.IPv4DNS2 != nil {
			info.DNS2 = st.IPv4DNS2.String()
		}
		info.MTU = st.MTU
	}
	if st6 := s.manager.SettingsV6(); st6 != nil && st6.IPv6Address != nil {
		info.IPv6Address = st6.IPv6Address.String()
	}
	return info, nil
}

// StartSOCKS5 启动 SOCKS5 代理(netstack,无需 admin)。需先 Dial。
func (s *DialerService) StartSOCKS5(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manager == nil {
		return fmt.Errorf("未拨号,请先拨号")
	}
	if s.bridge != nil {
		return fmt.Errorf("SOCKS5 已启动")
	}
	if addr == "" {
		addr = "127.0.0.1:1080"
	}

	bulkIn, bulkOut, err := s.transport.OpenBulkEndpoints()
	if err != nil {
		return fmt.Errorf("打开 bulk endpoints: %w", err)
	}

	st := s.manager.Settings()
	if st == nil || len(st.IPv4Address) == 0 {
		return fmt.Errorf("无 IPv4 地址")
	}
	ipBytes := st.IPv4Address.To4()
	localIP := netip.AddrFrom4([4]byte{ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3]})

	var v6Addr netip.Addr
	if st6 := s.manager.SettingsV6(); st6 != nil && len(st6.IPv6Address) > 0 {
		if v6, ok := netip.AddrFromSlice(st6.IPv6Address); ok {
			v6Addr = v6.Unmap()
		}
	}

	sink, err := qmidatapath.NewNetstackPacketSink(localIP, st.MTU, v6Addr.IsValid(), v6Addr)
	if err != nil {
		return fmt.Errorf("netstack: %w", err)
	}

	var dnsServers []netip.Addr
	if d, ok := netip.AddrFromSlice(st.IPv4DNS1); ok {
		dnsServers = append(dnsServers, d.Unmap())
	}
	if len(dnsServers) > 0 {
		sink.SetDNSServers(dnsServers)
	}

	bridge := qmidatapath.New(sink, bulkIn, bulkOut, st.MTU, true)
	if err := bridge.Start(s.ctx); err != nil {
		sink.Close()
		return fmt.Errorf("bridge: %w", err)
	}

	socksCtx, socksCancel := context.WithCancel(s.ctx)
	go func() {
		_ = qmidatapath.RunSOCKS5(socksCtx, sink, addr)
	}()

	s.sink = sink
	s.bridge = bridge
	s.socksCancel = socksCancel
	return nil
}

func (s *DialerService) stopSOCKS5Locked() {
	if s.socksCancel != nil {
		s.socksCancel()
		s.socksCancel = nil
	}
	if s.sink != nil {
		s.sink.Close()
		s.sink = nil
	}
	if s.bridge != nil {
		s.bridge.Stop()
		s.bridge = nil
	}
}

// StopSOCKS5 停止 SOCKS5 代理(保持拨号)。
func (s *DialerService) StopSOCKS5() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopSOCKS5Locked()
	return nil
}

// IsSOCKS5Running 返回 SOCKS5 是否在运行。
func (s *DialerService) IsSOCKS5Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bridge != nil
}

// GetStats 返回 relay 流量统计。
func (s *DialerService) GetStats() (*RelayStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bridge == nil {
		return &RelayStats{}, nil
	}
	txP, txB, rxP, rxB := s.bridge.Stats()
	return &RelayStats{TXPackets: txP, TXBytes: txB, RXPackets: rxP, RXBytes: rxB}, nil
}

// ── TUN 模式(系统级,需 root,通过 osascript sudo 启动 tun-helper 子进程)──

// StartTUN 启动 TUN 模式:先 build tun-helper binary,再 osascript sudo 启动(弹密码框)。
// tun-helper 以 root 独立进程运行(创建 utun + 拨号 + relay + DNS),app 监控其 PID。
func (s *DialerService) StartTUN(apn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tunPID != 0 && s.isTUNProcessAlive() {
		return fmt.Errorf("TUN 已运行(PID %d)", s.tunPID)
	}
	if apn == "" {
		apn = "3gnet"
	}

	// 1. Build tun-helper(非 root,go build)
	helperPath := filepath.Join(os.TempDir(), "dji-tun-helper")
	cwd, _ := os.Getwd()
	build := exec.Command("go", "build", "-o", helperPath, "./cmd/tun-helper")
	build.Dir = cwd
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build tun-helper: %w\n%s", err, out)
	}

	// 2. osascript sudo 启动(弹 macOS 密码框提权)
	script := fmt.Sprintf(
		`do shell script "%s -apn %s > /tmp/tun-helper.log 2>&1 & echo $!" with administrator privileges`,
		helperPath, apn,
	)
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return fmt.Errorf("TUN 提权失败(需输入管理员密码): %w", err)
	}

	// 3. 解析 PID
	pidStr := strings.TrimSpace(string(out))
	pid, _ := strconv.Atoi(pidStr)
	if pid == 0 {
		// fallback: 从 pid 文件读
		time.Sleep(2 * time.Second)
		pid = s.readTUNPIDFile()
	}
	s.tunPID = pid
	return nil
}

// StopTUN 停止 TUN(osascript sudo kill root 进程)。
func (s *DialerService) StopTUN() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tunPID == 0 {
		return nil
	}
	script := fmt.Sprintf(`do shell script "kill %d" with administrator privileges`, s.tunPID)
	_, _ = exec.Command("osascript", "-e", script).Output()
	s.tunPID = 0
	_ = os.Remove("/tmp/tun-helper.pid")
	return nil
}

// IsTUNRunning 返回 TUN helper 是否在运行。
func (s *DialerService) IsTUNRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tunPID != 0 && s.isTUNProcessAlive()
}

func (s *DialerService) isTUNProcessAlive() bool {
	if s.tunPID == 0 {
		return false
	}
	return syscall.Kill(s.tunPID, 0) == nil
}

func (s *DialerService) readTUNPIDFile() int {
	data, err := os.ReadFile("/tmp/tun-helper.pid")
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}
