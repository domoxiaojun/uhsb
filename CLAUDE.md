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
- WebUI 默认只对局域网开放（`CFWEB_ALLOW`，默认 `lan`）。`/api/run` 只接受白名单字段，禁止客户端指定 `SingBoxBinary`/`RootDir`；`/api/status` 不返回 `VMESS_UUID`。
- CSV 带 UTF-8 BOM。中断走 `HandleInterrupt`。
- `sing-box` 不编进 Go 模块，运行时 exec。每个候选 IP 一个临时 HTTP inbound，本地端口向系统动态申请。
