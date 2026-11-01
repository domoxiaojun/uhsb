#!/bin/sh
set -eu
mkdir -p /data/runs
if [ ! -f /data/locations.json ] && [ -f /app/locations.json ]; then
  cp /app/locations.json /data/locations.json
fi
if [ ! -f /data/ips-v4.txt ] && [ -f /app/ips-v4.txt ]; then
  cp /app/ips-v4.txt /data/ips-v4.txt
fi
exec "$@"
