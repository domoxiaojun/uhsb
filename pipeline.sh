#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNS_DIR="$ROOT_DIR/runs"

CFDATA_SCAN_THREADS=50
CFDATA_TEST_THREADS=25
CFDATA_PORT=443
CFDATA_DELAY=500
CFDATA_NUM_PER_COLO=0
CFDATA_DETAIL=y

CFSCAN_WORKERS=100
CFSCAN_RETRIES=1
CFSCAN_CYCLES=2
CFSCAN_TIMEOUT=10

CFSPEED_MODE=batch
CFSPEED_PROTOCOL=http
CFSPEED_SIZE=200
CFSPEED_WORKERS=20
CFSPEED_ROUNDS=3
CFSPEED_TIMEOUT=3
CFSPEED_LOSS=10
CFSPEED_SPEED=50
CFSPEED_LATENCY=200

CFCONNECTIVITY_WORKERS=20
CFCONNECTIVITY_ROUNDS=3
CFCONNECTIVITY_TIMEOUT=3
CFCONNECTIVITY_LOSS=10
CFCONNECTIVITY_SPEED=100
CFCONNECTIVITY_LATENCY=200
CFCONNECTIVITY_SIZE=200
CFCONNECTIVITY_ENABLED=n

LAST_IPS_FILE=""
LAST_QUALITY_FILE=""
LAST_SPEED_PREFIX=""
LAST_RUN_DIR=""

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

upper() {
  printf '%s' "$1" | tr '[:lower:]' '[:upper:]'
}

prompt_default() {
  local prompt="$1"
  local default_value="$2"
  local input
  read -r -p "$prompt" input
  input="$(trim "$input")"
  if [[ -z "$input" ]]; then
    printf '%s' "$default_value"
  else
    printf '%s' "$input"
  fi
}

prompt_required() {
  local prompt="$1"
  local input
  while true; do
    read -r -p "$prompt" input
    input="$(trim "$input")"
    if [[ -n "$input" ]]; then
      printf '%s' "$input"
      return
    fi
    echo "输入不能为空，请重试。"
  done
}

prompt_yes_no() {
  local prompt="$1"
  local default_value="$2"
  local input
  while true; do
    read -r -p "$prompt" input
    input="$(lower_or_default "$input" "$default_value")"
    case "$input" in
      y|n)
        printf '%s' "$input"
        return
        ;;
    esac
    echo "请输入 y 或 n。"
  done
}

lower_or_default() {
  local value="$1"
  local default_value="$2"
  value="$(trim "$value")"
  if [[ -z "$value" ]]; then
    printf '%s' "$default_value"
  else
    printf '%s' "$value" | tr '[:upper:]' '[:lower:]'
  fi
}

normalize_list() {
  local raw="$1"
  local token
  local result=()
  raw="${raw//,/ }"
  for token in $raw; do
    token="$(trim "$token")"
    if [[ -n "$token" ]]; then
      result+=("$(upper "$token")")
    fi
  done
  printf '%s\n' "${result[@]}"
}

join_by_underscore() {
  local first=1
  local item
  for item in "$@"; do
    if [[ $first -eq 1 ]]; then
      printf '%s' "$item"
      first=0
    else
      printf '_%s' "$item"
    fi
  done
}

ensure_nonempty_file() {
  local path="$1"
  local label="$2"
  if [[ ! -s "$path" ]]; then
    echo "[错误] $label 不存在或为空: $path" >&2
    exit 1
  fi
}

copy_if_exists() {
  local source="$1"
  local destination_dir="$2"
  if [[ -f "$source" ]]; then
    cp "$source" "$destination_dir/"
  fi
}

require_command() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "[错误] 缺少依赖命令: $name" >&2
    exit 1
  fi
}

run_go_tool() {
  local tool_dir="$1"
  shift
  (
    cd "$tool_dir"
    go run . "$@"
  )
}

prepare_environment() {
  require_command go
  require_command awk
  require_command cp
}

prepare_run_dir() {
  mkdir -p "$RUNS_DIR"
  LAST_RUN_DIR="$RUNS_DIR/$(date +%Y%m%d_%H%M%S)"
  mkdir -p "$LAST_RUN_DIR"
  echo "[信息] 本次运行目录: $LAST_RUN_DIR"
}

collect_inputs() {
  local mode_input
  local detail_default

  echo "=== Cloudflare 工具一键流水线 ==="

  while true; do
    mode_input="$(prompt_default "cfdata 模式 (1=指定Colo扫描, 2=筛固定Colo, 3=按国家筛选, 默认1): " "1")"
    case "$mode_input" in
      1|2|3)
        CFDATA_MODE="$mode_input"
        break
        ;;
      *)
        echo "请输入 1、2 或 3。"
        ;;
    esac
  done

  case "$CFDATA_MODE" in
    1)
      CFDATA_TARGET_RAW="$(prompt_required "目标 Colo (逗号或空格分隔，如 HKG NRT): ")"
      ;;
    2)
      CFDATA_TARGET_RAW="$(prompt_default "目标 Colo (默认 hkg,nrt,sin,tpa): " "hkg,nrt,sin,tpa")"
      ;;
    3)
      CFDATA_TARGET_RAW="$(prompt_required "目标国家代码 (逗号或空格分隔，如 HK JP): ")"
      ;;
  esac

  CFDATA_IPTYPE="$(prompt_default "IPv4还是IPv6? (4/6, 默认4): " "4")"
  if [[ "$CFDATA_IPTYPE" != "4" && "$CFDATA_IPTYPE" != "6" ]]; then
    echo "[错误] iptype 仅支持 4 或 6。" >&2
    exit 1
  fi

  CFDATA_SCAN_THREADS="$(prompt_default "扫描并发数 (默认 ${CFDATA_SCAN_THREADS}): " "$CFDATA_SCAN_THREADS")"
  CFDATA_TEST_THREADS="$(prompt_default "测试并发数 (默认 ${CFDATA_TEST_THREADS}): " "$CFDATA_TEST_THREADS")"
  CFDATA_PORT="$(prompt_default "测试端口 (默认 ${CFDATA_PORT}): " "$CFDATA_PORT")"
  CFDATA_DELAY="$(prompt_default "延迟阈值ms (默认 ${CFDATA_DELAY}): " "$CFDATA_DELAY")"
  CFDATA_NUM_PER_COLO="$(prompt_default "每个Colo查找IP数 (默认 ${CFDATA_NUM_PER_COLO}): " "$CFDATA_NUM_PER_COLO")"

  detail_default="$CFDATA_DETAIL"
  if [[ "$CFDATA_MODE" == "2" ]]; then
    detail_default="n"
  fi
  CFDATA_DETAIL="$(prompt_yes_no "是否跑 cfdata 详细测试? (y/n, 默认 ${detail_default}): " "$detail_default")"

  CFDATA_TARGET_TAG="$(build_cfdata_tag "$CFDATA_MODE" "$CFDATA_TARGET_RAW")"

  CFCONNECTIVITY_ENABLED="$(prompt_yes_no "是否追加 cfconnectivity 复核? (y/n, 默认 ${CFCONNECTIVITY_ENABLED}): " "$CFCONNECTIVITY_ENABLED")"
}

build_cfdata_tag() {
  local mode="$1"
  local raw="$2"
  local items=()
  while IFS= read -r line; do
    if [[ -n "$line" ]]; then
      items+=("$line")
    fi
  done < <(normalize_list "$raw")

  if [[ ${#items[@]} -eq 0 ]]; then
    echo "[错误] 目标值为空。" >&2
    exit 1
  fi

  local joined
  joined="$(join_by_underscore "${items[@]}")"
  if [[ "$mode" == "3" ]]; then
    printf 'country_%s' "$joined"
  else
    printf '%s' "$joined"
  fi
}

run_cfdata_stage() {
  local cfdata_dir="$ROOT_DIR/cfdata"
  local tag="$CFDATA_TARGET_TAG"
  local detail_flag=false
  local cfdata_args=()
  local ips_file_root="$cfdata_dir/${tag}_ips.txt"
  local ips_csv_root="$cfdata_dir/${tag}_ips.csv"
  local result_csv_root="$cfdata_dir/${tag}_result.csv"
  local zero_loss_csv_root="$cfdata_dir/${tag}_0loss.csv"
  local zero_loss_ips_root="$cfdata_dir/${tag}_0loss_ips.txt"

  echo
  echo "=== 阶段 1: cfdata ==="

  if [[ "$CFDATA_DETAIL" == "y" ]]; then
    detail_flag=true
  fi

  cfdata_args=(
    -batch
    -mode "$CFDATA_MODE"
    -iptype "$CFDATA_IPTYPE"
    -scan-threads "$CFDATA_SCAN_THREADS"
    -test-threads "$CFDATA_TEST_THREADS"
    -port "$CFDATA_PORT"
    -delay "$CFDATA_DELAY"
    -num-per-colo "$CFDATA_NUM_PER_COLO"
    -detail="$detail_flag"
  )

  case "$CFDATA_MODE" in
    1|2)
      cfdata_args+=(-colo "$CFDATA_TARGET_RAW")
      ;;
    3)
      cfdata_args+=(-country "$CFDATA_TARGET_RAW")
      ;;
  esac

  run_go_tool "$cfdata_dir" "${cfdata_args[@]}"

  copy_if_exists "$ips_file_root" "$LAST_RUN_DIR"
  copy_if_exists "$ips_csv_root" "$LAST_RUN_DIR"
  copy_if_exists "$result_csv_root" "$LAST_RUN_DIR"
  copy_if_exists "$zero_loss_csv_root" "$LAST_RUN_DIR"
  copy_if_exists "$zero_loss_ips_root" "$LAST_RUN_DIR"

  local run_ips_file="$LAST_RUN_DIR/$(basename "$ips_file_root")"
  local run_zero_loss_file="$LAST_RUN_DIR/$(basename "$zero_loss_ips_root")"

  ensure_nonempty_file "$run_ips_file" "cfdata 输出 IP 文件"

  if [[ "$CFDATA_DETAIL" == "y" && -s "$run_zero_loss_file" ]]; then
    LAST_IPS_FILE="$run_zero_loss_file"
  else
    LAST_IPS_FILE="$run_ips_file"
  fi

  echo "[信息] cfscan 输入文件: $LAST_IPS_FILE"
}

run_cfscan_stage() {
  local cfscan_dir="$ROOT_DIR/cfscan"
  local prefix="$LAST_RUN_DIR/$CFDATA_TARGET_TAG"

  echo
  echo "=== 阶段 2: cfscan ==="

  run_go_tool "$cfscan_dir" \
    -f "$LAST_IPS_FILE" \
    -o "$prefix" \
    -w "$CFSCAN_WORKERS" \
    -r "$CFSCAN_RETRIES" \
    -c "$CFSCAN_CYCLES" \
    -t "$CFSCAN_TIMEOUT"

  LAST_QUALITY_FILE="${prefix}_quality.txt"
  ensure_nonempty_file "$LAST_QUALITY_FILE" "cfscan 质量文件"
  echo "[信息] cfspeed 输入文件: $LAST_QUALITY_FILE"
}

run_cfspeed_stage() {
  local cfspeed_dir="$ROOT_DIR/cfspeed"
  LAST_SPEED_PREFIX="$LAST_RUN_DIR/${CFDATA_TARGET_TAG}_speed"

  echo
  echo "=== 阶段 3: cfspeed ==="

  run_go_tool "$cfspeed_dir" \
    -batch \
    -mode "$CFSPEED_MODE" \
    -protocol "$CFSPEED_PROTOCOL" \
    -f "$LAST_QUALITY_FILE" \
    -o "$LAST_SPEED_PREFIX" \
    -size "$CFSPEED_SIZE" \
    -w "$CFSPEED_WORKERS" \
    -rounds "$CFSPEED_ROUNDS" \
    -timeout "$CFSPEED_TIMEOUT" \
    -loss "$CFSPEED_LOSS" \
    -speed "$CFSPEED_SPEED" \
    -latency "$CFSPEED_LATENCY"

  ensure_nonempty_file "${LAST_SPEED_PREFIX}_results.csv" "cfspeed 结果文件"
}

build_connectivity_input() {
  local premium_file="${LAST_SPEED_PREFIX}_premium.txt"
  local cleaned_file="$LAST_RUN_DIR/${CFDATA_TARGET_TAG}_premium_ips.txt"

  if [[ -s "$premium_file" ]]; then
    awk '
      /^[[:space:]]*#/ { next }
      /^[[:space:]]*$/ { next }
      { print $1 }
    ' "$premium_file" > "$cleaned_file"
    if [[ -s "$cleaned_file" ]]; then
      printf '%s' "$cleaned_file"
      return
    fi
  fi

  printf '%s' "$LAST_QUALITY_FILE"
}

run_cfconnectivity_stage() {
  local cfconnectivity_dir="$ROOT_DIR/cfconnectivity"
  local connectivity_input
  local connectivity_prefix="$LAST_RUN_DIR/${CFDATA_TARGET_TAG}_connectivity"

  if [[ "$CFCONNECTIVITY_ENABLED" != "y" ]]; then
    return
  fi

  connectivity_input="$(build_connectivity_input)"
  ensure_nonempty_file "$connectivity_input" "cfconnectivity 输入文件"

  echo
  echo "=== 阶段 4: cfconnectivity ==="
  echo "[信息] cfconnectivity 输入文件: $connectivity_input"

  run_go_tool "$cfconnectivity_dir" \
    -batch \
    -f "$connectivity_input" \
    -o "$connectivity_prefix" \
    -w "$CFCONNECTIVITY_WORKERS" \
    -rounds "$CFCONNECTIVITY_ROUNDS" \
    -timeout "$CFCONNECTIVITY_TIMEOUT" \
    -loss "$CFCONNECTIVITY_LOSS" \
    -speed "$CFCONNECTIVITY_SPEED" \
    -latency "$CFCONNECTIVITY_LATENCY" \
    -size "$CFCONNECTIVITY_SIZE"

  ensure_nonempty_file "${connectivity_prefix}_full_results.csv" "cfconnectivity 结果文件"
}

print_summary() {
  echo
  echo "=== 完成 ==="
  echo "[保存] 运行目录: $LAST_RUN_DIR"
  echo "[保存] cfdata 输入链路文件: $LAST_IPS_FILE"
  echo "[保存] cfscan 质量文件: $LAST_QUALITY_FILE"
  echo "[保存] cfspeed 结果前缀: $LAST_SPEED_PREFIX"
  if [[ "$CFCONNECTIVITY_ENABLED" == "y" ]]; then
    echo "[保存] cfconnectivity 已执行"
  fi
}

prepare_environment
prepare_run_dir
collect_inputs
run_cfdata_stage
run_cfscan_stage
run_cfspeed_stage
run_cfconnectivity_stage
print_summary
