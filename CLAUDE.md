# CLAUDE.md

给在本仓库里改代码的助手看。用户面向文档是 `README.md`。

## 项目

`cftest`（Go 1.21+, 仅标准库）是 Cloudflare 优选 IP 工具链。默认交付是 Docker Compose + WebUI，镜像内置 sing-box。

```
cfdata → cfscan → cfspeed → (可选) cfconnectivity
```

引擎在 `internal/`，CLI 是薄包装，WebUI 在 `cfweb/`。

## 常用命令

```bash
go test ./...
docker compose up -d --build
go run ./cfweb -addr 127.0.0.1:8787 -root ./data -open=false
```

## 架构

- 引擎: `internal/cfdata` `cfscan` `cfspeed` `cfconnectivity` `cfpipeline` `singbox`
- CLI: `cfdata/` `cfscan/` `cfspeed/` `cfconnectivity/`
- Web: `cfweb/`（embed 静态页 + SSE）
- 共享: `internal/cfcommon`
- 部署: `Dockerfile` `docker-compose.yml` `.github/workflows/docker.yml`

改扫描/测速逻辑改 `internal/`，不要把业务写回 CLI main。

## 硬规则

- CF 禁止裸 IP 直连：`/cdn-cgi/trace` 必须带 `Host`（默认 `www.visa.cn`）。
- `locations.json` 是 colo→城市的唯一数据源。Docker 里放在 `/app/locations.json`，查找时优先于 `/data` 副本。
- CIDR 列表默认来自 `baipiao.eu.org`，缓存 `ips-v4.txt`（默认 24h 过期重下，离线用 `/app` 预置副本）。镜像预置一份到 `/app`。
- 直连测速默认 `cftest.domob.org`；sing-box 模式走真实 VMess+WS。
- `cfscan` 依赖系统 `mtr`（macOS/无 NET_RAW 时不可用会被探测并跳过）。镜像已装 `mtr-tiny`，并 `cap_add: NET_RAW`。
- `cfscan` 路由关键字可配（`Config.Keywords` / `-keywords` / `CFSCAN_KEYWORDS` / WebUI），逗号分隔，由 `CompileKeywords` 编译成 `\b(...)\b` 正则；默认 `hkg,tpe,tw,hk`，扫 NRT/SIN 需改成 `nrt,sin`。
- WebUI 默认只对局域网开放（`CFWEB_ALLOW`，默认 `lan`，只纳入回环/私有/链路本地网卡网段）。`/api/run` 只接受白名单字段，禁止客户端指定 `SingBoxBinary`/`RootDir`；`/api/status` 不返回 `VMESS_UUID`；`/api/health` 为不触发探测的容器探活端点。
- 阈值语义：显式 `0` = 关闭该门槛，`cfpipeline`/`cfspeed`/`cfconnectivity` 的 `withDefaults` 用 `< 0` 而非 `== 0`；未提供的字段由 cfweb 层显式填默认值。
- **默认值单一来源**：`internal/cfcommon/defaults.go`。CLI flag、四个引擎 `withDefaults`、`cfweb` 校验层都引用它的常量；WebUI 表单默认值由 `/api/status.defaults` 下发，**不要在 `cfweb/web/index.html` 里再写字面量默认值**。
- **HTTP 契约**：只读端点 GET、写端点 POST，统一 `requireMethod`；错误统一走 `writeError` 返回 JSON envelope + `X-Request-Id`，内部错误走 `internalError`（只记日志不回显路径）；`/api/run` 使用 `DisallowUnknownFields`；SSE 带 `id:` / `retry:`。
- 原子写：`TempWritePath` 生成唯一临时名，`ReplaceFileFromTemp` 优先单 rename、Windows 退化路径加互斥锁；不要在别处自己拼 `.tmp` / `.bak`。
- ctx 必须贯穿所有可能阻塞的 IO：`LoadLocationsCtx`、`GetColoCtx`、`MTRStatusCtx`、`loadSubnetListCtx`。`MTRStatusCtx` 的探测不持全局锁。
- `runs/<时间戳>` 目录用 `os.Mkdir` 挑唯一名；`ListRuns` 上限 `cfcommon.MaxRunList`。
- CSV 带 UTF-8 BOM。中断走 `HandleInterrupt`；`cfspeed` 交互菜单首次 Ctrl+C 取消当前测速而不是 `os.Exit`。
- 四个 CLI 失败时设置 `exitCode=1`，`main` 的 defer 里 `os.Exit`。
- `sing-box` 不编进 Go 模块，运行时 exec。每个候选 IP 一个临时 HTTP inbound，本地端口向系统动态申请；并发封顶 `singBoxMaxWorkers=6`。
- Compose 默认 `127.0.0.1:8787:8787`（`CFWEB_BIND_IP` / `CFWEB_PORT` 可覆盖），日志 `json-file` 10m×3 轮转。IPv6 种子 `ips-v6.txt` 必须入库并被 Dockerfile 复制到 `/app`。
- **凭据红线**：`TestResult.VMessURL` 带 `json:"-"`；SSE 只发 `resultEvent` 脱敏摘要；日志不打 `vmess://`；`*_vmess.txt` / `*_singbox.json` 用 `WriteLinesAtomicMode(..., 0600)`；`/api/files` 默认过滤并拒绝凭据产物（`CFWEB_EXPOSE_CREDENTIALS=true` 才放行），响应带 `Content-Disposition: attachment`。`isCredentialArtifact` 是唯一判定入口。
- **来源校验**：`wrap` 对全部端点做 `requestFromAllowedOrigin`（Host 白名单 + `Sec-Fetch-Site` + sameOrigin）。Host 只放行 IP 字面量、localhost 和 `CFWEB_HOSTS`；用域名访问必须配 `CFWEB_HOSTS`，否则 403。`CFWEB_ALLOW=any` 会启动告警并在 `/api/status.insecure` 标记。
- **进程生命周期**：cfweb 用 `http.Server`（`ReadHeaderTimeout`/`IdleTimeout`，不设 `WriteTimeout` 以保 SSE）+ `signal.NotifyContext(SIGINT,SIGTERM)` + `Shutdown`；`shutdownCh` 让 SSE 主动退出；`runPipeline` 必须带 `defer recover`。`singbox.Manager.procCtx` 是任务级 ctx，`session.close()` 调 `Manager.Close()` 兜底杀内核；`Instance.Close()` 有 3s 超时并返回 error。
- **写失败必须可见**：CSV 收尾统一走 `cfcommon.FinishCSVWrite`（无条件 close、聚合错误、清理临时文件），禁止再写 `writeErr != nil || closeFn() != nil` 这种短路。`CSVRecordSink` / `LineSink` 有 `firstErr`，`Close()` 会带上被 `_ =` 吞掉的写入错误。
- **容器非 root**：`USER cftest`(10001) + `setcap cap_net_raw+ep $(command -v mtr)`；compose 用 `CFWEB_UID`/`CFWEB_GID`；`scripts/entrypoint.sh` 启动时检查 `/data` 可写并给出 chown 提示。
- 布尔型可选参数（如 `VMessAllowInsecure`）一律用 `*bool`，否则显式 false 会被环境变量翻转；用 `boolOr` 回落。
