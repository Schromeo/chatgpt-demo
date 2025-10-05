#!/usr/bin/env bash
set -euo pipefail

CMD="${1:-up}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="$ROOT_DIR/logs"
PID_DIR="$ROOT_DIR/.pids"
mkdir -p "$LOG_DIR" "$PID_DIR"

# ===== 可配置环境变量（这里给出默认值，可在外部 export 覆盖）=====
export REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
export MYSQL_DSN="${MYSQL_DSN:-root:root@tcp(localhost:3306)/chatdb?parseTime=true&charset=utf8mb4,utf8}"
export OPENAI_MODEL="${OPENAI_MODEL:-gpt-4o-mini}"
DAILY_LIMIT="${DAILY_LIMIT:-5000}"    # tokenserver 每日限额（仅用于日志展示）
# OPENAI_API_KEY 必须由你在 shell 里 export；脚本不保存你的密钥

info(){ echo -e "\033[1;34m[INFO]\033[0m $*"; }
warn(){ echo -e "\033[1;33m[WARN]\033[0m $*"; }
err(){  echo -e "\033[1;31m[ERR ]\033[0m $*" >&2; }

regen_proto() {
  if command -v protoc >/dev/null 2>&1; then
    info "Generating protobuf stubs…"
    (cd "$ROOT_DIR" && protoc -I=proto --go_out=. --go-grpc_out=. proto/chat.proto)
  else
    warn "protoc not found, skip proto generation."
  fi
}

need_key() {
  if [[ -z "${OPENAI_API_KEY:-}" ]]; then
    warn "OPENAI_API_KEY not set. llmserver will fail to call OpenAI. Export it before running."
  fi
}

is_running() {
  local name="$1"
  [[ -f "$PID_DIR/$name.pid" ]] && kill -0 "$(cat "$PID_DIR/$name.pid")" 2>/dev/null
}

start_one() {
  local name="$1"; shift
  local cmd="$*"
  if is_running "$name"; then
    info "$name already running (pid $(cat "$PID_DIR/$name.pid"))."
    return
  fi
  info "Starting $name …"
  (cd "$ROOT_DIR" && bash -lc "$cmd") >"$LOG_DIR/$name.log" 2>&1 &
  echo $! > "$PID_DIR/$name.pid"
  sleep 0.2
}

stop_one() {
  local name="$1"
  if is_running "$name"; then
    info "Stopping $name (pid $(cat "$PID_DIR/$name.pid"))"
    kill "$(cat "$PID_DIR/$name.pid")" 2>/dev/null || true
    rm -f "$PID_DIR/$name.pid"
  fi
}

start_all() {
  regen_proto
  need_key
  # 依次启动（本地依赖假定已就绪：Redis, MySQL）
  start_one tokenserver  "go run ./tokenserver"
  start_one historyserver "go run ./historyserver"
  start_one filterserver "go run ./filterserver"
  start_one llmserver    "go run ./llmserver"
  start_one gateway      "go run ./gateway"
  info "All services started."
  info "Redis: $REDIS_ADDR | MySQL: $MYSQL_DSN | DailyLimit: $DAILY_LIMIT | Model: ${OPENAI_MODEL}"
  info "Tail logs:   tail -f $LOG_DIR/*.log"
}

stop_all() {
  stop_one gateway
  stop_one llmserver
  stop_one filterserver
  stop_one historyserver
  stop_one tokenserver
  info "All services stopped."
}

status() {
  for n in tokenserver historyserver filterserver llmserver gateway; do
    if is_running "$n"; then
      echo "✔ $n (pid $(cat "$PID_DIR/$n.pid")) log: $LOG_DIR/$n.log"
    else
      echo "✖ $n (stopped)"
    fi
  done
}

logs() {
  local n="${1:-}"
  if [[ -z "$n" ]]; then
    info "Tailing all logs… (Ctrl+C to exit)"
    tail -F "$LOG_DIR"/*.log
  else
    [[ -f "$LOG_DIR/$n.log" ]] || { err "No log for $n"; exit 1; }
    tail -F "$LOG_DIR/$n.log"
  fi
}

deps_up() {
  if ! command -v docker >/dev/null 2>&1; then
    err "Docker not found. Either install Docker or run Redis/MySQL locally."
    exit 1
  fi
  # Redis
  if ! docker ps --format '{{.Names}}' | grep -q '^chatdemo-redis$'; then
    info "Starting Redis (docker)…"
    docker run -d --name chatdemo-redis -p 6379:6379 redis:7 >/dev/null
  else
    info "Redis container already running."
  fi
  # MySQL (root/root) + init.sql（如果存在）
  if ! docker ps --format '{{.Names}}' | grep -q '^chatdemo-mysql$'; then
    info "Starting MySQL (docker)…"
    local mnt=""
    if [[ -f "$ROOT_DIR/sql/init.sql" ]]; then
      mnt="-v $ROOT_DIR/sql/init.sql:/docker-entrypoint-initdb.d/init.sql:ro"
    fi
    docker run -d --name chatdemo-mysql -p 3306:3306 -e MYSQL_ROOT_PASSWORD=root $mnt mysql:8 >/dev/null
  else
    info "MySQL container already running."
  fi
  info "Deps up. Redis=localhost:6379  MySQL=localhost:3306 (root/root)"
}

deps_down() {
  if command -v docker >/dev/null 2>&1; then
    docker rm -f chatdemo-redis chatdemo-mysql >/dev/null 2>&1 || true
    info "Deps down."
  fi
}

usage() {
  cat <<EOF
Usage: $(basename "$0") [command] [args]

Commands:
  up              Start all services (background)        # 默认
  down            Stop all services
  restart         Stop then start
  status          Show running status
  logs [name]     Tail logs (all or one of: tokenserver|historyserver|filterserver|llmserver|gateway)
  deps up         Start Redis & MySQL via Docker
  deps down       Stop Redis & MySQL containers

Env (override as needed):
  OPENAI_API_KEY   (required for llmserver)
  OPENAI_MODEL     default: $OPENAI_MODEL
  REDIS_ADDR       default: $REDIS_ADDR
  MYSQL_DSN        default: $MYSQL_DSN

Examples:
  OPENAI_API_KEY=sk-xxx scripts/dev.sh up
  scripts/dev.sh deps up && scripts/dev.sh up
  scripts/dev.sh logs gateway
  scripts/dev.sh down
EOF
}

case "$CMD" in
  up) start_all ;;
  down) stop_all ;;
  restart) stop_all; start_all ;;
  status) status ;;
  logs) shift || true; logs "${1:-}";;
  deps)
    sub="${2:-}"; shift || true
    case "$sub" in
      up) deps_up ;;
      down) deps_down ;;
      *) usage; exit 1 ;;
    esac
    ;;
  *) usage; exit 1 ;;
esac
