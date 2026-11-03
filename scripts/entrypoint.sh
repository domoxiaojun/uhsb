#!/bin/sh
set -eu
# locations.json 与 ips-v4.txt 都直接使用镜像内 /app 的副本（LoadLocations /
# cfdata 的查找逻辑优先 /app），不再复制到 /data 造成"旧副本遮蔽新数据"。
mkdir -p /data/runs
exec "$@"
