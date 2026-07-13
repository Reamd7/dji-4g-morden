// Package services 实现 desktop 的 Wails 后端服务。
// DeviceService 负责 USB 设备发现 + 手动启用(打开 AT 通道)。
package services

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/google/gousb"
	"github.com/wailsapp/wails/v3/pkg/application"

	"dji-modem-research/internal/usbtransport"
	"dji-modem-research/third_party/sms-gateway/modem"
)

const (
	djiVID    gousb.ID = 0x2C7C // Quectel Wireless Solutions
	ec25PID   gousb.ID = 0x0125 // EC25 LTE modem(标准 PID,刷机后)
	mi02Iface         = 2       // MI_02 AT 命令口
	mi02EpOut         = 0x03    // bulk OUT
	mi02EpIn          = 0x84    // bulk IN
)

// USBDeviceInfo 描述一个发现的 USB 设备,序列化后传给前端。
type USBDeviceInfo struct {
	VID        string `json:"vid"`        // "2c7c"
	PID        string `json:"pid"`        // "0125"
	Product    string `json:"product"`    // iProduct 字符串
	Vendor     string `json:"vendor"`     // iManufacturer 字符串
	Serial     string `json:"serial"`     // iSerial 字符串
	Interfaces int    `json:"interfaces"` // 接口数(EC25 模式通常 5)
}

// DeviceService 提供 USB 设备发现 + 手动启用(打开 AT 通道)能力。
type DeviceService struct {
	mu        sync.Mutex
	modem     *modem.Modem          // 启用后持有的 AT modem(nil=未启用)
	transport *usbtransport.ATTransport
}

func (s *DeviceService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	return nil
}

// ReportDOM 接收前端上报的 DOM 快照(开发期调试自动化:写 /tmp 供外部检查)。
func (s *DeviceService) ReportDOM(dom string) {
	_ = os.WriteFile("/tmp/desktop-dom.html", []byte(dom), 0644)
}

// ListDevices 枚举所有 DJI 百望 4G 模组(VID 2C7C),返回描述符信息。
// 不长期持有设备句柄 — 每次调用独立开闭,供前端刷新设备列表。
func (s *DeviceService) ListDevices() ([]USBDeviceInfo, error) {
	ctx := gousb.NewContext()
	defer ctx.Close()

	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return desc.Vendor == djiVID
	})
	if err != nil {
		return nil, fmt.Errorf("枚举 USB 设备失败: %w", err)
	}
	defer func() {
		for _, d := range devs {
			_ = d.Close()
		}
	}()

	var out []USBDeviceInfo
	for _, d := range devs {
		product, _ := d.Product()
		vendor, _ := d.Manufacturer()
		serial, _ := d.SerialNumber()
		ifaceCount := 0
		if cfg, ok := d.Desc.Configs[0]; ok {
			ifaceCount = len(cfg.Interfaces)
		}
		out = append(out, USBDeviceInfo{
			VID:        fmt.Sprintf("%04x", uint16(d.Desc.Vendor)),
			PID:        fmt.Sprintf("%04x", uint16(d.Desc.Product)),
			Product:    product,
			Vendor:     vendor,
			Serial:     serial,
			Interfaces: ifaceCount,
		})
	}
	return out, nil
}

// Connect 手动启用设备:打开 MI_02 AT 通道并初始化 modem(AT→ATE0→CMEE→CPIN→CMGF→CNMI→CPMS)。
// 启用后设备进入可用状态,后续可做 AT 命令/短信/设备信息查询。
// 重复启用返回错误(需先 Disconnect)。
func (s *DeviceService) Connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modem != nil {
		return fmt.Errorf("设备已启用,请先断开")
	}
	transport, err := usbtransport.Open(uint16(djiVID), uint16(ec25PID), mi02Iface, mi02EpOut, mi02EpIn)
	if err != nil {
		return fmt.Errorf("打开 AT 通道(MI_02)失败: %w", err)
	}
	m := modem.NewFromIO(transport)
	if err := m.Initialize(context.Background(), ""); err != nil {
		_ = transport.Close()
		return fmt.Errorf("modem 初始化失败: %w", err)
	}
	s.transport = transport
	s.modem = m
	return nil
}

// Disconnect 断开设备(关闭 modem,内部释放 AT transport)。
func (s *DeviceService) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modem == nil {
		return nil
	}
	err := s.modem.Close()
	s.modem = nil
	s.transport = nil
	return err
}

// IsConnected 返回设备是否已启用(AT 通道是否打开)。
func (s *DeviceService) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modem != nil
}

// DeviceInfo 是启用后可查询的设备详细信息(每字段对应一条 AT 命令)。
type DeviceInfo struct {
	IMEI      string `json:"imei"`
	ICCID     string `json:"iccid"`
	IMSI      string `json:"imsi"`
	Carrier   string `json:"carrier"`
	Phone     string `json:"phone"`
	Firmware  string `json:"firmware"`
	SignalDBm int    `json:"signalDbm"`
	SignalOk  bool   `json:"signalOk"`
}

// GetDeviceInfo 查询已启用设备的详细信息(IMEI/ICCID/IMSI/运营商/本机号/固件/信号 dBm)。
// 需先 Connect;每字段一条 AT 命令,整体约 3-5 秒。
func (s *DeviceService) GetDeviceInfo() (*DeviceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modem == nil {
		return nil, fmt.Errorf("设备未启用,请先启用")
	}
	ctx := context.Background()
	imsi, _ := s.modem.IMSI(ctx)
	fw, _ := s.modem.SoftwareVersion(ctx)
	rssi, ok := s.modem.SignalDBm(ctx)
	return &DeviceInfo{
		IMEI:      s.modem.IMEI(ctx),
		ICCID:     s.modem.ICCID(ctx),
		IMSI:      imsi,
		Carrier:   s.modem.Carrier(ctx),
		Phone:     s.modem.PhoneNumber(ctx),
		Firmware:  fw,
		SignalDBm: rssi,
		SignalOk:  ok,
	}, nil
}
