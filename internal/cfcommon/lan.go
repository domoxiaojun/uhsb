package cfcommon

import (
	"fmt"
	"net"
	"strings"
)

// AccessControl 决定某个客户端 IP 是否允许访问 WebUI。
// 默认策略 "lan": 仅允许回环 + 私有网段 + 本机的局域网网卡网段，
// 这样局域网内的其它机器可以访问，公网地址（含公网网卡同网段）会被拒绝。
type AccessControl struct {
	allowAny bool
	nets     []*net.IPNet
	desc     string
}

var privateCIDRs = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"fc00::/7",
	"fe80::/10",
	"::1/128",
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// NewAccessControl 解析 spec:
//
//	"" / "lan"  -> 回环 + 私有网段 + 本机网卡网段
//	"private"   -> 回环 + 私有网段
//	"any" / "*" -> 允许全部（不推荐）
//	其它        -> 逗号分隔的 CIDR / IP / "loopback" / "private"
func NewAccessControl(spec string) (*AccessControl, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "lan") {
		ac := &AccessControl{desc: "lan"}
		for _, c := range privateCIDRs {
			ac.nets = append(ac.nets, mustCIDR(c))
		}
		ac.nets = append(ac.nets, localInterfaceCIDRs()...)
		return ac, nil
	}
	if strings.EqualFold(spec, "any") || spec == "*" {
		return &AccessControl{allowAny: true, desc: "any"}, nil
	}
	ac := &AccessControl{desc: spec}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch {
		case strings.EqualFold(part, "private"):
			for _, c := range privateCIDRs {
				ac.nets = append(ac.nets, mustCIDR(c))
			}
		case strings.EqualFold(part, "loopback"):
			ac.nets = append(ac.nets, mustCIDR("127.0.0.0/8"), mustCIDR("::1/128"))
		default:
			n := parseIPOrCIDR(part)
			if n == nil {
				return nil, fmt.Errorf("无法解析白名单条目: %q", part)
			}
			ac.nets = append(ac.nets, n)
		}
	}
	if len(ac.nets) == 0 {
		return nil, fmt.Errorf("白名单为空: %q", spec)
	}
	return ac, nil
}

func parseIPOrCIDR(s string) *net.IPNet {
	if strings.Contains(s, "/") {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil
		}
		return n
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil
	}
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
}

func localInterfaceCIDRs() []*net.IPNet {
	var out []*net.IPNet
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet == nil || ipnet.IP == nil {
			continue
		}
		// 只纳入本机局域网/回环网卡网段。VPS 上公网网卡的 CIDR 若被加入
		// 白名单，等于把同网段公网机器也放进了无认证的 WebUI。
		if !isLANIP(ipnet.IP) {
			continue
		}
		out = append(out, ipnet)
	}
	return out
}

func isLANIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// Allowed 判断远端地址是否在白名单内。remoteAddr 形如 "192.168.1.5:1234"。
func (a *AccessControl) Allowed(remoteAddr string) bool {
	if a == nil || a.allowAny {
		return true
	}
	ip := RemoteIP(remoteAddr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, n := range a.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// RemoteIP 从 "ip:port" 提取客户端 IP，兼容 IPv6 方括号形式。
func RemoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

func (a *AccessControl) String() string {
	if a == nil {
		return "nil"
	}
	return a.desc
}

// Insecure 报告当前是否完全放行来源。启动与 /api/status 用它给出显式告警，
// 避免「any + 0.0.0.0」把无认证 WebUI 悄悄暴露到公网。
func (a *AccessControl) Insecure() bool {
	return a != nil && a.allowAny
}
