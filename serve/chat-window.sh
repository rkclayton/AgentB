#!/usr/bin/env sh
set -eu

session_id=${1:-main}
url="http://127.0.0.1:8790/chat?session=$session_id"
for browser in google-chrome google-chrome-stable chromium chromium-browser microsoft-edge; do
  if command -v "$browser" >/dev/null 2>&1; then
    "$browser" "--app=$url" --window-size=520,760 >/dev/null 2>&1 &
    exit 0
  fi
done
if command -v open >/dev/null 2>&1; then
  open "$url"
elif command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$url"
else
  printf '%s\n' "$url"
fi
