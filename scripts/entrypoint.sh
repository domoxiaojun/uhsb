#!/bin/sh
set -eu
# locations.json / ips-v4.txt / ips-v6.txt 都直接使用镜像内 /app 的副本
# （LoadLocations 与 cfdata 的查找逻辑优先 /app），不再复制到 /data
# 造成"旧副本遮蔽新数据"。
mkdir -p /data/runs 2>/dev/null || true
# 镜像以非 root 运行；宿主机 bind mount 的 ./data 若属主不匹配会写入失败。
# 与其在测速中途莫名报"保存失败"，不如启动时给一条可操作的提示。
if [ ! -w /data ] || [ ! -w /data/runs ]; then
  echo "[错误] /data 对容器用户不可写，运行产物无法保存。" >&2
  echo "  Linux 宿主机请执行: mkdir -p data && sudo chown -R ${CFWEB_UID:-10001}:${CFWEB_GID:-10001} data" >&2
  echo "  或在 .env 里把 CFWEB_UID / CFWEB_GID 设为 ./data 的属主 UID/GID。" >&2
  exit 1
fi
exec "$@"
