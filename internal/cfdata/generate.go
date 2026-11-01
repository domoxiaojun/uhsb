package cfdata

import (
	"context"
	"math/rand"
	"net"
	"strings"
	"time"
)

func countGeneratedIPs(subnets []string, isIPv6 bool, ipv6PerSubnet int) int {
	total := 0
	for _, subnet := range subnets {
		subnet = strings.TrimSpace(subnet)
		if subnet == "" {
			continue
		}
		if isIPv6 {
			if _, _, err := net.ParseCIDR(subnet); err == nil {
				total += ipv6PerSubnet
			}
		} else {
			total += countExpandedIPv4Subnet(subnet)
		}
	}
	return total
}

func forEachGeneratedIP(ctx context.Context, subnets []string, isIPv6 bool, ipv6PerSubnet int, yield func(string) bool) {
	ordered := append([]string(nil), subnets...)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(ordered), func(i, j int) {
		ordered[i], ordered[j] = ordered[j], ordered[i]
	})

	for _, subnet := range ordered {
		subnet = strings.TrimSpace(subnet)
		if subnet == "" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if isIPv6 {
			if !forEachIPv6InSubnet(ctx, rng, subnet, ipv6PerSubnet, yield) {
				return
			}
			continue
		}
		if !forEachIPv4InSubnet(ctx, rng, subnet, yield) {
			return
		}
	}
}

func countExpandedIPv4Subnet(cidr string) int {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0
	}
	ones, bits := ipNet.Mask.Size()
	hostBits := bits - ones
	if hostBits > 16 {
		hostBits = 16
	}
	numHosts := 1 << hostBits
	if numHosts <= 2 {
		return 0
	}
	return numHosts - 2
}

func forEachIPv4InSubnet(ctx context.Context, rng *rand.Rand, cidr string, yield func(string) bool) bool {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return true
	}
	count := countExpandedIPv4Subnet(cidr)
	if count == 0 {
		return true
	}

	base := make(net.IP, len(ipNet.IP))
	copy(base, ipNet.IP)
	start := 0
	if count > 1 {
		start = rng.Intn(count)
	}
	step := chooseCoprimeStep(rng, count)
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			return false
		}
		offset := ((start + i*step) % count) + 1
		newIP := make(net.IP, len(base))
		copy(newIP, base)
		for j := len(newIP) - 1; j >= 0; j-- {
			newIP[j] = base[j] | byte((offset>>(8*(len(newIP)-1-j)))&0xff)
		}
		if ipNet.Contains(newIP) && !yield(newIP.String()) {
			return false
		}
	}
	return true
}

func chooseCoprimeStep(rng *rand.Rand, n int) int {
	if n <= 1 {
		return 1
	}
	step := rng.Intn(n-1) + 1
	for gcd(step, n) != 1 {
		step++
		if step >= n {
			step = 1
		}
	}
	return step
}

func forEachIPv6InSubnet(ctx context.Context, rng *rand.Rand, cidr string, count int, yield func(string) bool) bool {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return true
	}
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			return false
		}
		ip := make(net.IP, 16)
		copy(ip, ipNet.IP)
		ones, _ := ipNet.Mask.Size()
		startByte := ones / 8
		for j := startByte; j < 16; j++ {
			ip[j] = byte(rng.Intn(256))
		}
		if ipNet.Contains(ip) && !yield(ip.String()) {
			return false
		}
	}
	return true
}
