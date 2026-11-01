package cfdata

import (
	"time"

	"cftest/internal/cfcommon"
)

const (
	DefaultIPv4URL = "https://www.baipiao.eu.org/cloudflare/ips-v4"
	DefaultIPv6URL = "https://www.baipiao.eu.org/cloudflare/ips-v6"
	DefaultHost    = "www.visa.cn"
)

type Config struct {
	Mode          string
	Colo          string
	Country       string
	IPType        string
	Port          int
	Delay         int
	ScanThreads   int
	TestThreads   int
	NumPerColo    int
	Detail        bool
	IPv6PerSubnet int
	HostHeader    string
	WorkDir       string
	IPv4URL       string
	IPv6URL       string
}

func (c Config) withDefaults() Config {
	if c.IPType == "" {
		c.IPType = "4"
	}
	if c.Port == 0 {
		c.Port = 443
	}
	if c.Delay == 0 {
		c.Delay = 500
	}
	if c.ScanThreads <= 0 {
		c.ScanThreads = 50
	}
	if c.TestThreads <= 0 {
		c.TestThreads = 25
	}
	if c.IPv6PerSubnet <= 0 {
		c.IPv6PerSubnet = 100
	}
	if c.HostHeader == "" {
		c.HostHeader = DefaultHost
	}
	if c.IPv4URL == "" {
		c.IPv4URL = DefaultIPv4URL
	}
	if c.IPv6URL == "" {
		c.IPv6URL = DefaultIPv6URL
	}
	return c
}

type ScanResult struct {
	IP, DataCenter, Region, City, CountryCode, LatencyStr string
	TCPDuration                                           time.Duration
}

type DetailedTestResult struct {
	IP, DataCenter, Region, City, CountryCode string
	MinLatency, MaxLatency, AvgLatency        time.Duration
	LossRate                                  float64
}

type Result struct {
	ScanResults    []ScanResult
	DetailResults  []DetailedTestResult
	ZeroLoss       []DetailedTestResult
	IPFile         string
	CSVFile        string
	ResultCSV      string
	ZeroLossCSV    string
	ZeroLossIPFile string
	Tag            string
}

func pathIn(cfg Config, name string) string {
	return cfcommon.WorkPath(cfg.WorkDir, name)
}
