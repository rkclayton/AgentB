#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HERE="$ROOT/serve/probes/reliability"
GO="${GO:-go}"
LLAMA_SERVER="${LLAMA_SERVER:-llama-server}"
C1_MODEL="${C1_MODEL:?set C1_MODEL to the Q3 model path}"
C2_MODEL="${C2_MODEL:?set C2_MODEL to the IQ4_XS model path}"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
RUNS="$HERE/runs"
SERVER_PID=""

stop_server() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -TERM -- "-$SERVER_PID" 2>/dev/null || kill -TERM "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  SERVER_PID=""
}
trap stop_server EXIT

start_server() {
  local name="$1" model="$2" context="$3" fit="${4:-off}"
  stop_server
  setsid "$LLAMA_SERVER" -m "$model" --alias qwen3.8-27b --host 127.0.0.1 --port 8080 \
    -c "$context" -ngl 99 -fa on -ctk q8_0 -ctv q8_0 --fit "$fit" --no-mmproj \
    --parallel 1 --jinja --reasoning-format auto --slots --metrics --temp 0.6 \
    --top-p 0.95 --top-k 20 --min-p 0.0 --log-file "$RUNS/$name-server.log" &
  SERVER_PID=$!
  for _ in $(seq 1 480); do
    curl -fsS "$BASE_URL/health" >/dev/null 2>&1 && return 0
    kill -0 "$SERVER_PID" 2>/dev/null || return 1
    sleep 0.5
  done
  return 1
}

run_trials() {
  local name="$1" run_dir="$RUNS/$1"
  mkdir -p "$run_dir"
  find "$run_dir" -maxdepth 1 -type f -name '*.jsonl' -delete
  for task in fix add; do
    for trial in 1 2 3; do
      local workspace="$run_dir/workspace-$task-$trial"
      "$GO" run ./cmd/fixture "$workspace"
      "$GO" run ./cmd/loop --base-url "$BASE_URL" --model qwen3.8-27b \
        --workspace "$workspace" --task "$task" --out "$run_dir/$task-$trial.jsonl" \
        --temperature 0.6 --effort medium --max-turns 12
    done
  done
  "$GO" run ./cmd/score "$run_dir" | tee "$run_dir/score.md"
}

mkdir -p "$RUNS"
cd "$HERE"
"$GO" build ./...
"$GO" vet ./...
start_server c1 "$C1_MODEL" 32768
run_trials c1
stop_server
start_server c2 "$C2_MODEL" 16384 off || start_server c2 "$C2_MODEL" 16384 on
run_trials c2
