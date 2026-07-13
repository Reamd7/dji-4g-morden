// SMSService 提供短信收发能力,复用 DeviceService 已启用的 modem 句柄。
package services

import (
	"context"
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"

	"dji-modem-research/third_party/sms-gateway/modem"
)

// SMS 是一条解码后的短信(前端展示用)。
type SMS struct {
	Index     int    `json:"index"`     // SIM 存储索引(AT+CMGD 用)
	Sender    string `json:"sender"`    // 发件人 MSISDN
	Body      string `json:"body"`      // 正文(UCS-2/GSM7 自动解码)
	Timestamp string `json:"timestamp"` // 服务中心时间戳
}

// SMSService 复用 DeviceService.modem(同包可直接访问私有字段)。
type SMSService struct {
	Device *DeviceService
}

func (s *SMSService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	return nil
}

// ListSMS 读取 SIM 已存短信并解码(AT+CMGL=4 → DecodeDeliver,UCS-2/GSM7 自动)。
func (s *SMSService) ListSMS() ([]SMS, error) {
	s.Device.mu.Lock()
	defer s.Device.mu.Unlock()
	if s.Device.modem == nil {
		return nil, fmt.Errorf("设备未启用,请先启用")
	}
	stored, err := s.Device.modem.ListStored(context.Background())
	if err != nil {
		return nil, fmt.Errorf("读取短信失败: %w", err)
	}
	var out []SMS
	for _, sm := range stored {
		d, err := modem.DecodeDeliver(sm.PDU)
		if err != nil {
			continue // 跳过无法解码的(如非 DELIVER 类型)
		}
		out = append(out, SMS{
			Index:     sm.Index,
			Sender:    d.Sender,
			Body:      d.Body,
			Timestamp: d.Timestamp.Format("2006-01-02 15:04:05"),
		})
	}
	return out, nil
}

// SendSMS 发送短信(长短信自动分段,内部 CMGS 两步握手)。
func (s *SMSService) SendSMS(recipient, body string) error {
	s.Device.mu.Lock()
	defer s.Device.mu.Unlock()
	if s.Device.modem == nil {
		return fmt.Errorf("设备未启用,请先启用")
	}
	return s.Device.modem.Send(context.Background(), recipient, body)
}

// DeleteSMS 删除 SIM 上指定索引的短信(AT+CMGD)。
func (s *SMSService) DeleteSMS(index int) error {
	s.Device.mu.Lock()
	defer s.Device.mu.Unlock()
	if s.Device.modem == nil {
		return fmt.Errorf("设备未启用,请先启用")
	}
	return s.Device.modem.DeleteStored(context.Background(), index)
}
