#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -f "$SCRIPT_DIR/local.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$SCRIPT_DIR/local.env"
  set +a
fi

MODEL_PATH=${MODEL_PATH:-}
LLAMA_SERVER=${LLAMA_SERVER:-llama-server}
MODEL_ALIAS=${MODEL_ALIAS:-local}
CTX=${CTX:-32768}
KV_TYPE=${KV_TYPE:-q8_0}
PORT=${PORT:-8080}
BIND_ADDRESS=${BIND_ADDRESS:-127.0.0.1}
MTP=${MTP:-off}
MTP_MODEL_PATH=${MTP_MODEL_PATH:-}
TOKEN_EMBEDDING_CPU=${TOKEN_EMBEDDING_CPU:-off}
FIT=${FIT:-off}
LOG_FILE=${LOG_FILE:-}

if [ -z "$MODEL_PATH" ]; then
  echo "MODEL_PATH is unset; copy serve/local.env.example to serve/local.env and set MODEL_PATH." >&2
  exit 1
fi
if [ ! -f "$MODEL_PATH" ]; then
  echo "model not found: $MODEL_PATH" >&2
  exit 1
fi
if ! command -v "$LLAMA_SERVER" >/dev/null 2>&1 && [ ! -x "$LLAMA_SERVER" ]; then
  echo "llama-server not found: $LLAMA_SERVER" >&2
  exit 1
fi

set -- \
  -m "$MODEL_PATH" \
  --alias "$MODEL_ALIAS" \
  --host "$BIND_ADDRESS" \
  --port "$PORT" \
  -c "$CTX" \
  -ngl 99 \
  -fa on \
  -ctk "$KV_TYPE" \
  -ctv "$KV_TYPE" \
  --fit "$FIT" \
  --no-mmproj \
  --parallel 1 \
  --jinja \
  --reasoning-format auto \
  --slots \
  --metrics \
  --temp 0.6 \
  --top-p 0.95 \
  --top-k 20 \
  --min-p 0.0

if [ "$TOKEN_EMBEDDING_CPU" = on ]; then
  set -- "$@" -ot token_embd.weight=CPU
fi
if [ -n "$LOG_FILE" ]; then
  mkdir -p "$(dirname -- "$LOG_FILE")"
  set -- "$@" --log-file "$LOG_FILE" --log-colors off
fi
if [ "$MTP" = on ]; then
  set -- "$@" --spec-type draft-mtp --spec-draft-n-max 2 --spec-draft-type-k "$KV_TYPE" --spec-draft-type-v "$KV_TYPE"
  if [ -n "$MTP_MODEL_PATH" ] && [ -f "$MTP_MODEL_PATH" ]; then
    set -- "$@" -md "$MTP_MODEL_PATH"
  elif [ -n "$MTP_MODEL_PATH" ]; then
    echo "warning: MTP requested but separate draft model is absent: $MTP_MODEL_PATH" >&2
  fi
fi

echo "Starting llama-server on http://$BIND_ADDRESS:$PORT"
echo "Model: $MODEL_PATH"
echo "Context: $CTX; KV: $KV_TYPE; MTP: $MTP; token embedding on CPU: $TOKEN_EMBEDDING_CPU; fit: $FIT"
exec "$LLAMA_SERVER" "$@"
