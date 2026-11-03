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
docker compose up -d --build
```

打开 [http://127.0.0.1:8787](http://127.0.0.1:8787)。运行产物在 `./data/runs/`。

WebUI 默认只对**局域网**开放（回环 + 私有网段 + 本机网卡网段），公网地址会被拒绝；可用 `CFWEB_ALLOW` 收紧到具体网段，例如 `CFWEB_ALLOW=192.168.1.0/24`。接口没有账号体系，请勿直接暴露到公网。`/api/status` 不会返回 `VMESS_UUID`，只告知前端“服务端已配置”。

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
| `CFWEB_ADDR` | `0.0.0.0:8787` | WebUI 监听 |
| `CFWEB_ROOT` | `/data` | 运行数据目录 |
| `CFWEB_ALLOW` | `lan` | 允许访问的来源：`lan`(回环+私有网段+本机网卡) / `private` / `any` / 逗号分隔 CIDR |
| `VMESS_UUID` | 空 | 你的节点 UUID |
| `VMESS_HOST` | 空 | WS Host |
| `VMESS_PATH` | `/` | WS path |
| `VMESS_SNI` | 同 Host | TLS SNI |
| `VMESS_PORT` | `443` | 端口 |
| `VMESS_TLS` | `true` | 是否 TLS |
| `CFTEST_IMAGE` | `cftest:local` | compose 使用的镜像名 |

这是客户端测速，不是给你搭服务端。UUID/Host/Path 必须是你已有的节点参数。

## 本地开发

```bash
go test ./...
go run ./cfweb -addr 127.0.0.1:8787
```

需要系统里有 `sing-box`（测 VMess）和 `mtr`（cfscan）。Windows 上没有 mtr 时 WebUI 会默认跳过路由扫描。

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
- `ips-v4.txt`（CIDR 列表缓存，默认 24 小时过期后重新下载；离线则用镜像预置副本）

GitHub Actions 在 `main` / tag `v*` 上构建 `linux/amd64` + `linux/arm64` 并推送到 `ghcr.io/domoxiaojun/uhsb`。打开仓库 **Packages**，把 `uhsb` 包设为 public 后即可匿名拉取。
