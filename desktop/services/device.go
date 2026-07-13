// Package services 实现 desktop 的 Wails 后端服务。
// DeviceService 负责 USB 设备发现(枚举 DJI 百望 4G 模组)。
package services

import (
	"context"
	"fmt"

	"github.com/google/gousb"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// djiVID 是 Quectel Wireless Solutions 的厂商 ID(DJI 百望模组刷标准 EC25 PID 后归属此 VID)。
const djiVID gousb.ID = 0x2C7C

// USBDeviceInfo 描述一个发现的 USB 设备,序列化后传给前端。
type USBDeviceInfo struct {
	VID        string `json:"vid"`        // "2c7c"
	PID        string `json:"pid"`        // "0125"
	Product    string `json:"product"`    // iProduct 字符串
	Vendor     string `json:"vendor"`     // iManufacturer 字符串
	Serial     string `json:"serial"`     // iSerial 字符串
	Interfaces int    `json:"interfaces"` // 接口数(EC25 模式通常 5:MI_00..MI_04)
}

// DeviceService 提供 USB 设备发现能力。
type DeviceService struct{}

func (s *DeviceService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	return nil
}

// ListDevices 枚举所有 DJI 百望 4G 模组(VID 2C7C),返回描述符信息。
// 不长期持有设备句柄 — 每次调用独立开闭,供前端刷新设备列表。
func (s *DeviceService) ListDevices() ([]USBDeviceInfo, error) {
	ctx := gousb.NewContext()
	defer ctx.Close()

	// OpenDevices 的回调返回 true 表示打开该设备;我们只开 VID 2C7C 的。
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
