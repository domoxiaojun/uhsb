package cfcommon

import "time"

// 全项目共享的默认值。CLI flag、WebUI 表单、cfweb 校验层、pipeline 与四个引擎的
// withDefaults 都必须引用这里，避免同一参数在不同入口出现不同默认值。
// 改默认值只改本文件；WebUI 通过 /api/status 的 defaults 字段从这里取值。
const (
	DefaultHost          = "www.visa.cn"
	DefaultPort          = 443
	DefaultSpeedHost     = "cftest.domob.org"
	DefaultSpeedSize     = 50 // MB
	DefaultWorkers       = 20
	DefaultScanThreads   = 50
	DefaultTestThreads   = 25
	DefaultIPv6PerSubnet = 100
	DefaultCacheMaxAge   = 24 * time.Hour

	DefaultMinSpeedMbps       = 50
	DefaultMaxPacketLoss      = 10
	DefaultMaxLatency         = 200
	DefaultTestRounds         = 3
	DefaultConnectTimeout     = 3
	DefaultDelay              = 500
	DefaultJitterBaseline     = 100.0
	DefaultColoRetries        = 1
	DefaultSingleSpeedTimeout = 30
	DefaultEarlyMinSpeed      = 10.0
	DefaultEarlyCheckDur      = 3 * time.Second

	// DefaultScanWorkers 是 CLI 直跑 cfscan 的并发。
	// PipelineScanWorkers 是编排里的并发：容器里同时跑太多 mtr 会被限速/丢包，
	// 这个差异是有意的，但必须是具名常量而不是散落的字面量。
	DefaultScanWorkers     = 100
	PipelineScanWorkers    = 16
	DefaultHighQualityHops = 16
	DefaultKeywords        = "hkg,tpe,tw,hk"
	DefaultMTRRetries      = 1
	DefaultMTRCycles       = 2
	DefaultMTRTimeout      = 10

	DefaultConnSpeed      = 100
	DefaultConnLoss       = 10
	DefaultConnWorkers    = 20
	DefaultConnRounds     = 3
	DefaultConnTimeout    = 3
	DefaultConnLatency    = 200
	DefaultConnSize       = 200
	DefaultPreferredColos = "hkg,nrt,sin,tpe,icn"

	DefaultVMessPort          = 443
	DefaultVMessPath          = "/"
	DefaultVMessTLS           = true
	DefaultVMessAllowInsecure = false

	DefaultDataMode      = "1"
	DefaultIPType        = "4"
	DefaultSpeedMode     = "batch"
	DefaultSpeedProtocol = "http"
	DefaultTargetColo    = "HKG,NRT"
	DefaultWebNumPerColo = 8 // WebUI 默认限量，避免一次扫出过多 IP；0 = 不限

	DefaultCFWebAddr  = "127.0.0.1:8787"
	DefaultCFWebAllow = "lan"
	DefaultCFWebOpen  = true
)

// 取值范围上限：cfweb 校验层与 pipeline 共用，避免前端与后端各自拍脑袋。
const (
	MaxNumPerColo      = 100000
	MaxSpeedSize       = 1000
	MaxScanThreads     = 200
	MaxTestThreads     = 100
	MaxSpeedWorkers    = 50
	MaxConnWorkers     = 50
	MaxConnSize        = 1000
	MaxHighQualityHops = 1000
	MaxRunList         = 200
)
