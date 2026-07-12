package netcfg

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
)

// DarwinConfigurator implements NetworkConfigurator for macOS
// DarwinConfigurator 为 macOS 实现 NetworkConfigurator
type DarwinConfigurator struct {
	// v4addr stashes the IPv4 local address from SetIPAddress. macOS utun is
	// point-to-point and needs a destination (the gateway) to configure inet,
	// which only arrives in AddDefaultRoute — so IPv4 is configured there.
	v4addr net.IP
}

func NewDarwinConfigurator() *DarwinConfigurator {
	return &DarwinConfigurator{}
}

func (d *DarwinConfigurator) run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("command %s failed: %s, output: %s", name, err, string(out))
	}
	return nil
}

func (d *DarwinConfigurator) SetIPAddress(ifname string, ip net.IP, prefixLen int) error {
	if ip.To4() != nil {
		// Defer IPv4: macOS utun is point-to-point and requires a destination
		// address (the gateway) to configure inet. Both "alias" and plain
		// "inet IP/PREFIX" fail on a primary-less utun (observed: utun ends up
		// with inet6 but no inet). Stash local; configure in AddDefaultRoute.
		d.v4addr = ip
		return nil
	}
	// inet6 alias works on utun (observed: inet6 set successfully).
	return d.run("ifconfig", ifname, "inet6", fmt.Sprintf("%s/%d", ip.String(), prefixLen), "alias")
}

func (d *DarwinConfigurator) SetIPv6Address(ifname string, ip net.IP, prefixLen int) error {
	// ifconfig ifname inet6 IP/Prefix alias
	return d.run("ifconfig", ifname, "inet6", fmt.Sprintf("%s/%d", ip.String(), prefixLen), "alias")
}

func (d *DarwinConfigurator) FlushAddresses(ifname string) error {
	// Can't easily flush all, so we might skip or rely on down/up
	// 无法轻易清除所有，所以我们可能跳过或依赖 down/up
	return nil
}

func (d *DarwinConfigurator) AddDefaultRoute(ifname string, gateway net.IP) error {
	// Configure the deferred point-to-point IPv4 (local + gateway as dest),
	// then add the default route via -iface (link-direct, no ARP on pt-to-pt).
	if gateway.To4() != nil {
		if d.v4addr != nil {
			if err := d.run("ifconfig", ifname, "inet", d.v4addr.String(), gateway.String()); err != nil {
				return err
			}
		}
		// macOS default route already exists (main network en0); "route add
		// default" fails with "File exists". Use the VPN-standard split: 0/1 +
		// 128/1 cover all of 0.0.0.0/0, are more specific than the existing
		// default, and take precedence without removing it. FlushRoutes
		// removes them on disconnect, restoring the main default.
		if err := d.run("route", "-n", "add", "0/1", "-iface", ifname); err != nil {
			return err
		}
		return d.run("route", "-n", "add", "128/1", "-iface", ifname)
	}
	return d.run("route", "-n", "add", "-inet6", "default", "-iface", ifname)
}

func (d *DarwinConfigurator) AddDefaultRouteDirect(ifname string, ipv6 bool) error {
	if ipv6 {
		return d.run("route", "add", "-inet6", "default", "-interface", ifname)
	}
	return d.run("route", "add", "default", "-interface", ifname)
}

func (d *DarwinConfigurator) FlushRoutes(ifname string) error {
	// Remove the split default routes (0/1 + 128/1) added by AddDefaultRoute.
	// Ignore errors — routes may already be gone once the utun closes.
	d.run("route", "-n", "delete", "0/1", "-iface", ifname)
	d.run("route", "-n", "delete", "128/1", "-iface", ifname)
	return nil
}

func (d *DarwinConfigurator) BringUp(ifname string) error {
	return d.run("ifconfig", ifname, "up")
}

func (d *DarwinConfigurator) BringDown(ifname string) error {
	return d.run("ifconfig", ifname, "down")
}

func (d *DarwinConfigurator) SetMTU(ifname string, mtu int) error {
	return d.run("ifconfig", ifname, "mtu", strconv.Itoa(mtu))
}

func (d *DarwinConfigurator) GetCurrentIP(ifname string) (net.IP, error) {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP, nil
			}
		}
	}
	return nil, nil
}

func (d *DarwinConfigurator) IsUp(ifname string) (bool, error) {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return false, err
	}
	return iface.Flags&net.FlagUp != 0, nil
}

func (d *DarwinConfigurator) UpdateDNS(dns1, dns2 string) error {
	// macOS uses networksetup or scutil, modifying /etc/resolv.conf is not recommended but might work temporarily
	// macOS 使用 networksetup 或 scutil，修改 /etc/resolv.conf 不推荐但可能暂时有效
	// For simplicity in this CLI tool, we skip system-wide DNS modification on macOS
	return nil
}

func (d *DarwinConfigurator) RestoreDNS() error {
	return nil
}

// QMAP 多路复用在 macOS 上不支持
func (d *DarwinConfigurator) AddQMAPMux(masterIface string, muxID uint8) (string, error) {
	return "", fmt.Errorf("QMAP 多路复用在 macOS 上不可用")
}
func (d *DarwinConfigurator) DelQMAPMux(masterIface string, muxID uint8) error       { return nil }
func (d *DarwinConfigurator) GetQMAPMuxIface(masterIface string, muxID uint8) string { return "" }
func (d *DarwinConfigurator) EnableRawIP(ifname string) error                        { return nil }
