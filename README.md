# cftest

Cloudflare 优选 IP 工具链：扫 colo → 路由质量 → 测速评分。默认用法是 **Docker Compose + WebUI**，镜像内置 `sing-box` 和 `mtr`。

```
cfdata → cfscan → cfspeed → (可选) cfconnectivity
                ↘ sing-box VMess+WS 真实路径测速 → vmess:// 节点
```

## 快速开始

```bash
git clone https://github.com/domoxiaojun/uhsb.git
cd uhsb
cp .env.example .env
# 可选：填 VMESS_UUID / VMESS_HOST，WebUI 会自动勾上 sing-box 测速
mkdir -p data
# Linux 需要让容器内的非 root 用户（UID 10001）能写挂载目录：
sudo chown -R 10001:10001 data    # 或在 .env 里设 CFWEB_UID=$(id -u) / CFWEB_GID=$(id -g)
docker compose up -d --build
```

打开 [http://127.0.0.1:8787](http://127.0.0.1:8787)。运行产物在 `./data/runs/`。镜像以非 root 用户运行，`mtr` 靠文件 capability + `NET_RAW` 获得 raw socket 权限。

**容器使用 host 网络**，直接复用宿主机网络栈，IPv4/IPv6 都能测速。**默认只监听宿主机回环**（`CFWEB_ADDR=127.0.0.1:8787`）。要从局域网访问，在 `.env` 里设 `CFWEB_ADDR=0.0.0.0:8787`，同时把 `CFWEB_ALLOW` 收紧到具体网段（如 `192.168.1.0/24`）。WebUI 默认只对**局域网**开放，公网来源（含公网网卡同网段）会被 403 拒绝；接口没有账号体系，请勿直接暴露到公网。`/api/status` 不会返回 `VMESS_UUID`，只告知前端“服务端已配置”；容器健康检查走 `/api/health`（不触发 mtr / sing-box 探测）。

Web 页面可直接设置：测速大小、最低 Mbps、最大丢包率、高跳数阈值（`-1` 关闭该启发式）、cfscan 路由关键字、TLS 与允许无效证书。阈值填 `0` 表示关闭该门槛，不会再被重置为默认值。**表单默认值由 `/api/status` 的 `defaults` 下发**，来源统一是 `internal/cfcommon/defaults.go`，改默认值只需改一处。

**凭据产物**：`*_vmess.txt` / `*_singbox.json` 含明文 UUID，落盘权限 `0600`，且默认**不出现在 `/api/files` 列表、也不允许经 Web 下载**（返回 403）。请从宿主机 `./data/runs/` 取用；确有需要才在 `.env` 里设 `CFWEB_EXPOSE_CREDENTIALS=true`。日志与 SSE 事件只传脱敏结果摘要，不含 `vmess://` 链接。所有端点还校验 Host 白名单：用 IP 直连即可，若用自定义域名访问需设 `CFWEB_HOSTS=你的域名`，否则返回 403。

API 约定：只读端点（`/api/status`、`/api/health`、`/api/runs`、`/api/files`、`/api/events`）只接受 `GET`，写端点（`/api/run`、`/api/stop`）只接受 `POST`；错误统一返回 JSON `{"error":{"code","message","request_id"}}` 并带 `X-Request-Id` 响应头；`/api/run` 带未知字段（如 `SingBoxBinary`）会直接 400，不会静默忽略。4 个 CLI 失败时返回非零退出码，便于脚本/CI 判断。

**cfscan 关键字**：默认 `hkg,tpe,tw,hk`（港台）。扫其它 colo 时改成对应关键字即可，例如 `nrt,sin`；逗号分隔，只允许字母、数字、`-`、`_`。CLI 用 `./cfscan -keywords nrt,sin -f xxx_ips.txt`，服务端可用环境变量 `CFSCAN_KEYWORDS` 统一预填。

从 GitHub Container Registry 拉镜像（仓库推送后）：

```bash
# 在 .env 里加：CFTEST_IMAGE=ghcr.io/domoxiaojun/uhsb:latest
docker compose pull
docker compose up -d
```

首次推送前，GitHub 仓库 **Settings → Actions → General** 打开 workflow，并确认 `GITHUB_TOKEN` 能写 `packages`。镜像地址是 `ghcr.io/domoxiaojun/uhsb`。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `CFTEST_IMAGE` | `cftest:local` | compose 使用的镜像名（可设 GHCR 地址） |
| `CFWEB_ADDR` | `127.0.0.1:8787` | WebUI 监听地址（host 网络，直接生效）；局域网访问设 `0.0.0.0:8787` |
| `CFWEB_ROOT` | `/data` | 运行数据目录 |
| `CFWEB_ALLOW` | `lan` | 允许访问的来源：`lan`(回环+私有网段+本机网卡) / `private` / `any` / 逗号分隔 CIDR；设 `any` 会在启动日志与页面顶部告警 |
| `CFWEB_HOSTS` | 空 | 额外允许的 Host（逗号分隔）。用域名访问时必须配置，防 DNS rebinding |
| `CFWEB_UID` / `CFWEB_GID` | `10001` | 容器运行用户，需与 `./data` 属主匹配 |
| `CFWEB_EXPOSE_CREDENTIALS` | `false` | 是否允许经 Web 下载含 UUID 的节点产物 |
| `CFSCAN_KEYWORDS` | `hkg,tpe,tw,hk` | cfscan 路由关键字 |
| `VMESS_UUID` | 空 | 你的节点 UUID |
| `VMESS_HOST` | 空 | WS Host |
| `VMESS_PATH` | `/` | WS path |
| `VMESS_SNI` | 同 Host | TLS SNI |
| `VMESS_PORT` | `443` | 端口 |
| `VMESS_TLS` | `true` | 是否 TLS |
| `VMESS_ALLOW_INSECURE` | `false` | 跳过 TLS 校验（仅自签证书节点） |

这是客户端测速，不是给你搭服务端。UUID/Host/Path 必须是你已有的节点参数。完整模板见 `.env.example`。

## 本地开发

```bash
go test -count=1 ./...
go vet ./...
go run ./cfweb -addr 127.0.0.1:8787 -open=false
```

需要系统里有 `sing-box`（测 VMess）和 `mtr`（cfscan）。Windows 上没有 mtr 时 WebUI 会默认跳过路由扫描。GitHub Actions 在 Linux 上额外跑 `go test -race ./...`（本机 Windows 若无 gcc 可跳过）。

CLI 仍可用：

```bash
go run ./cfdata -mode 1 -colo HKG,NRT -batch
go run ./cfspeed -singbox -uuid ... -ws-host ... -f quality.txt -batch
```

## 镜像内容

- `cfweb` 入口 + `cfdata` / `cfscan` / `cfspeed` / `cfconnectivity`
- 官方 `sing-box` 内核（构建参数 `SINGBOX_VERSION`）
- `mtr-tiny`
- `locations.json`（镜像内 `/app/locations.json` 为唯一数据源，优先于 `/data` 旧副本）
- `ips-v4.txt` / `ips-v6.txt`（IPv4、IPv6 的 Cloudflare 官方网段种子，放 `/app/`；CIDR 缓存默认 24 小时过期后重新下载，下载失败或离线时回退到预置副本）

容器日志用 `json-file` 并限制 `10m × 3` 轮转；`/api/runs` 最多返回最近 200 次运行。

GitHub Actions 在 `main` / tag `v*` 上先跑 `go test`、`go vet`、`go test -race`，再构建 `linux/amd64` + `linux/arm64` 并推送到 `ghcr.io/domoxiaojun/uhsb`。打开仓库 **Packages**，把 `uhsb` 包设为 public 后即可匿名拉取。

## 运行保障

- `cfweb` 用 `http.Server` + SIGINT/SIGTERM 优雅停机：收到信号后取消当前任务、断开 SSE 连接并 `Shutdown`；后台流水线 goroutine 有 `recover`，panic 会重置忙碌状态而不是拖垮整个进程。
- `sing-box` 子进程受任务级 ctx 兜底，`Instance.Close()` 有超时并清理含凭据的临时配置；`NewManager` 会清掉上次异常退出残留的配置。
- 结果 CSV 写入失败会显式报错（不再静默返回成功），实时 sink 的写入错误会在关闭时汇总上报。
