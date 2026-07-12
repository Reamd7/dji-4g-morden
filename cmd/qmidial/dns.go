package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// dnsRestore holds the platform-specific DNS restore function, set by
// configureDNS on macOS (where DNS is written to a shared network service)
// and invoked by restoreDNS on disconnect, so the main network's DNS isn't
// left pointing at the 4G carrier DNS (unreachable over Wi-Fi).
var dnsRestore func()

// configureDNS sets DNS servers on the TUN interface.
// netcfg.UpdateDNS is broken on Windows (error stub) and macOS (no-op),
// so we do it ourselves per-platform.
func configureDNS(tunName, dns1, dns2 string) error {
	switch runtime.GOOS {
	case "windows":
		return configureDNSWindows(tunName, dns1, dns2)
	case "darwin":
		return configureDNSDarwin(tunName, dns1, dns2)
	case "linux":
		return configureDNSLinux(tunName, dns1, dns2)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// restoreDNS reverts the DNS changes made by configureDNS (macOS). No-op when
// no DNS was configured (e.g. non-TUN mode or non-darwin).
func restoreDNS() {
	if dnsRestore != nil {
		dnsRestore()
		dnsRestore = nil
	}
}

func configureDNSWindows(tunName, dns1, dns2 string) error {
	// netsh interface ip set dns name="<tun>" static <dns1> primary
	if err := exec.Command("netsh", "interface", "ip", "set", "dns",
		fmt.Sprintf("name=%s", tunName), "static", dns1, "primary").Run(); err != nil {
		return fmt.Errorf("netsh set dns primary: %w", err)
	}
	if dns2 != "" {
		// netsh interface ip add dns name="<tun>" <dns2> index=2
		if err := exec.Command("netsh", "interface", "ip", "add", "dns",
			fmt.Sprintf("name=%s", tunName), dns2, "index=2").Run(); err != nil {
			// Non-fatal — primary DNS is enough
			fmt.Printf("  warn: netsh add dns secondary: %v\n", err)
		}
	}
	return nil
}

func configureDNSDarwin(tunName, dns1, dns2 string) error {
	// macOS networksetup targets a "network service" (e.g. Wi-Fi/Ethernet),
	// not the utun interface — setting DNS here overrides the main network's
	// resolver. Snapshot the original first and register a restore, so the
	// main network's DNS isn't left polluted with the 4G carrier DNS after
	// disconnect. Restore: put back the original servers, or "empty" (= DHCP).
	services := []string{"Ethernet", "Wi-Fi", "USB 10/100/1000 LAN"}
	for _, svc := range services {
		origOut, _ := exec.Command("networksetup", "-getdnsservers", svc).Output()
		orig := strings.TrimSpace(string(origOut))
		anySet := orig != "" && !strings.Contains(orig, "There aren't any DNS servers")
		if err := exec.Command("networksetup", "-setdnsservers", svc, dns1, dns2).Run(); err != nil {
			continue
		}
		// Capture per-iteration values for the closure.
		svcName, wasAny, origVals := svc, anySet, orig
		dnsRestore = func() {
			if wasAny {
				args := append([]string{"-setdnsservers", svcName}, strings.Fields(origVals)...)
				exec.Command("networksetup", args...).Run()
			} else {
				exec.Command("networksetup", "-setdnsservers", svcName, "empty").Run()
			}
		}
		return nil
	}
	return fmt.Errorf("could not set DNS on any known network service")
}

func configureDNSLinux(tunName, dns1, dns2 string) error {
	// Try resolvectl first (systemd-resolved)
	if path, err := exec.LookPath("resolvectl"); err == nil {
		args := []string{"dns", tunName, dns1}
		if dns2 != "" {
			args = append(args, dns2)
		}
		if err := exec.Command(path, args...).Run(); err == nil {
			return nil
		}
	}
	// Fallback: write /etc/resolv.conf directly
	return configureDNSResolvConf(dns1, dns2)
}

func configureDNSResolvConf(dns1, dns2 string) error {
	// This is a simple fallback — netcfg already does this on Linux.
	// Only use if resolvectl is unavailable.
	content := fmt.Sprintf("nameserver %s\n", dns1)
	if dns2 != "" {
		content += fmt.Sprintf("nameserver %s\n", dns2)
	}
	// Write to /etc/resolv.conf (may need root)
	if err := exec.Command("sh", "-c", fmt.Sprintf("echo '%s' > /etc/resolv.conf", content)).Run(); err != nil {
		return fmt.Errorf("write resolv.conf: %w", err)
	}
	return nil
}
