#!/usr/bin/env bash
# Real LLM generate on the sample fixture. Run this yourself.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ -d "/c/Users/Administrator/scoop/apps/go/current" ]]; then
  export GOROOT="/c/Users/Administrator/scoop/apps/go/current"
  export PATH="$GOROOT/bin:$PATH"
fi

FIX="${1:-$ROOT/testdata/e2e-sample}"
LOG="${2:-$ROOT/testdata/e2e-generate.log}"

if [[ ! -x "$ROOT/wikify.exe" && ! -x "$ROOT/wikify" ]]; then
  echo "building wikify..."
  go build -ldflags "-s -w -X main.appVersion=0.1.0" -o wikify.exe .
fi
BIN="$ROOT/wikify.exe"
[[ -x "$BIN" ]] || BIN="$ROOT/wikify"

echo "binary: $BIN"
echo "target: $FIX"
echo "log:    $LOG"
echo "config: $(ls -la ~/.wikify/config.yaml 2>/dev/null || echo missing)"

"$BIN" config 2>/dev/null | head -12 || true

echo ""
echo ">>> generate (max-pages=20, workers=3)"
"$BIN" generate \
  --dir "$FIX" \
  -y \
  --draft clear \
  --max-pages 20 \
  --lang Chinese \
  --workers 3 \
  --retries 2 \
  --verbose-catalog \
  2>&1 | tee "$LOG"

echo ""
echo ">>> polish (offline normalize)"
"$BIN" polish --dir "$FIX" --export-lang zh

echo ""
echo ">>> quick checks"
ls -la "$FIX/.wikify/meta/" || true
find "$FIX/.wikify/content" -name '*.md' 2>/dev/null | wc -l
echo "browse: $BIN browse --dir $FIX"
