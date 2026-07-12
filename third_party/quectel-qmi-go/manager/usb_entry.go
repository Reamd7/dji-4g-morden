package manager

import (
	"context"

	"dji-modem-research/third_party/quectel-qmi-go/qmi"
)

// NewWithClient creates a Manager that uses a pre-constructed QMI client
// instead of opening /dev/cdc-wdm0. This is the USB transport injection point:
// the caller constructs a client via qmi.NewClientFromTransport with a
// qmitransport.QMITransport (model B: EP0 control encapsulation), then passes
// it here. The Manager bypasses Linux device discovery entirely and can run
// on Windows/macOS via direct USB.
//
// Ownership: the Manager takes ownership of the client. On Stop() or failed
// Start(), cleanup() will call client.Close(), which closes the underlying
// transport. The caller must NOT close the client separately.
//
// Recovery limitation: the manager's auto-reconnect calls the same hook, which
// reuses the same client. If the USB transport is disrupted (e.g. device
// unplugged), recovery will fail. Full USB recovery (re-open transport +
// re-create client) is a stage 3 concern.
func NewWithClient(cfg Config, logger Logger, client *qmi.Client) *Manager {
	m := New(cfg, logger)
	m.openClientAndAllocateServicesHook = func(ctx context.Context) error {
		m.mu.Lock()
		m.client = client
		m.mu.Unlock()
		return m.allocateServices(ctx)
	}
	return m
}

// SettingsV6 returns the IPv6 runtime settings, or nil if IPv6 is not
// connected. Call after Connect() with EnableIPv6: true.
func (m *Manager) SettingsV6() *qmi.RuntimeSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settingsV6
}

// HandleV6 returns the IPv6 packet data handle (non-zero if IPv6 connected).
func (m *Manager) HandleV6() uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handleV6
}

// HandleV4 returns the IPv4 packet data handle (non-zero if IPv4 connected).
func (m *Manager) HandleV4() uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handleV4
}
