package util

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"epos-proxy/internal/logger"
)

const LOCALHOST_IP = "127.0.0.1"

type NetworkInfo struct {
	IP             string
	Subnet         string
	Interface      string
	ActiveFirewall string
	Zone           string
}

type ipv4Candidate struct {
	ip        net.IP
	ipNet     *net.IPNet
	ifaceName string
	score     int
}

func getRouteIPv4() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil
	}
	return addr.IP.To4()
}

func isLikelyVirtualInterface(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{
		"utun", "awdl", "llw", "bridge", "docker", "veth", "virbr",
		"vmnet", "tailscale", "tun", "tap", "wg",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isLikelyPhysicalInterface(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{"en", "eth", "wlan", "wi-fi", "wifi", "ethernet"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func localIPv4Candidates() []ipv4Candidate {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	routeIP := getRouteIPv4()
	candidates := make([]ipv4Candidate, 0)
	seen := make(map[string]struct{})

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast() {
				continue
			}

			key := ip.String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			score := 0
			if ip.IsPrivate() {
				score += 100
			}
			if isLikelyPhysicalInterface(iface.Name) {
				score += 60
			}
			if isLikelyVirtualInterface(iface.Name) {
				score -= 80
			}
			if routeIP != nil && routeIP.Equal(ip) {
				score += 25
			}

			candidates = append(candidates, ipv4Candidate{
				ip:        ip,
				ipNet:     ipNet,
				ifaceName: iface.Name,
				score:     score,
			})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].ifaceName != candidates[j].ifaceName {
			return candidates[i].ifaceName < candidates[j].ifaceName
		}
		return candidates[i].ip.String() < candidates[j].ip.String()
	})

	return candidates
}

func getLocalIPv4() net.IP {
	candidates := localIPv4Candidates()
	if len(candidates) > 0 {
		return candidates[0].ip
	}

	// Keep the previous default-route fallback for unusual hosts where
	// interface enumeration does not expose the address.
	return getRouteIPv4()
}

// GetLocalIPv4Addresses returns all useful non-loopback IPv4 addresses ordered
// from the most likely physical LAN interface to virtual/VPN interfaces.
func GetLocalIPv4Addresses() []string {
	candidates := localIPv4Candidates()
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.ip.String())
	}

	if len(result) == 0 {
		if routeIP := getRouteIPv4(); routeIP != nil && !routeIP.IsLoopback() {
			result = append(result, routeIP.String())
		}
	}
	return result
}

func localAddrInfo() (ip net.IP, ipNet *net.IPNet, ifaceName string) {
	candidates := localIPv4Candidates()
	if len(candidates) == 0 {
		ip = getRouteIPv4()
		return ip, nil, ""
	}

	best := candidates[0]
	return best.ip, best.ipNet, best.ifaceName
}

// formatSubnet returns the network CIDR string for the given IP and mask,
// e.g. "192.168.1.0/24".
func formatSubnet(ip net.IP, mask net.IPMask) string {
	ones, _ := mask.Size()
	return fmt.Sprintf("%s/%d", ip.Mask(mask), ones)
}

// runCmd runs the named command with args, returning its stdout.
// Returns an error if the command is not found or exits non-zero.
func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func getFirewalldZone(ifaceName string) string {
	out, err := runCmd("firewall-cmd", "--get-zone-of-interface="+ifaceName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func getFirewallManager() string {
	if runtime.GOOS != "linux" {
		return runtime.GOOS
	}
	for _, unit := range []string{"ufw", "firewalld", "nftables"} {
		if exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil {
			return unit
		}
	}
	return ""
}

func GetNetworkInfo() NetworkInfo {
	ip, ipNet, ifaceName := localAddrInfo()

	info := NetworkInfo{
		IP:             LOCALHOST_IP,
		Interface:      ifaceName,
		ActiveFirewall: getFirewallManager(),
	}

	if ip != nil {
		info.IP = ip.String()
	}

	if ipNet != nil {
		info.Subnet = formatSubnet(ip, ipNet.Mask)
	}

	if ifaceName != "" && info.ActiveFirewall == "firewalld" {
		info.Zone = getFirewalldZone(ifaceName)
	}

	return info
}

func GetLocalIP(isNetworkEnabled bool) string {
	if !isNetworkEnabled {
		return LOCALHOST_IP
	}

	logger.Debugf("Detecting local LAN IP address...")
	if ip := getLocalIPv4(); ip != nil {
		logger.Debugf("Selected LAN IP: %v", ip)
		return ip.String()
	}

	logger.Warnf("No usable LAN IPv4 address found, falling back to localhost")
	return LOCALHOST_IP
}
