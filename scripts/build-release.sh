#!/usr/bin/env bash
# Build multi-platform release packages into ./dist
# Usage: bash scripts/build-release.sh
# Optional: VERSION=0.1.1 bash scripts/build-release.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.1.0}"

LDFLAGS="-s -w -X main.appVersion=${VERSION}"

echo "→ tests"
go test ./internal/...

echo "→ clean dist"
rm -rf dist
mkdir -p dist

zip_dir() {
  local name=$1
  if command -v zip >/dev/null 2>&1; then
    (cd dist && zip -q -r "${name}.zip" "${name}")
    return
  fi
  py -3 -c "
from pathlib import Path
import zipfile
name = '''${name}'''
dist = Path('dist')
d = dist / name
with zipfile.ZipFile(dist / f'{name}.zip', 'w', zipfile.ZIP_DEFLATED) as zf:
    for p in d.rglob('*'):
        if p.is_file():
            zf.write(p, p.relative_to(dist).as_posix())
print('zipped', name)
"
}

build_one() {
  local goos=$1 goarch=$2
  local name="wikify_${VERSION}_${goos}_${goarch}"
  local dir="dist/${name}"
  mkdir -p "$dir"
  local out="$dir/wikify"
  if [[ "$goos" == "windows" ]]; then
    out="$dir/wikify.exe"
  fi
  echo "→ build ${goos}/${goarch}"
  GOOS=$goos GOARCH=$goarch go build -ldflags "$LDFLAGS" -o "$out" .
  cp LICENSE "$dir/" 2>/dev/null || true
  cp README.md "$dir/" 2>/dev/null || true
  if [[ "$goos" == "windows" ]]; then
    zip_dir "$name"
  else
    (cd dist && tar -czf "${name}.tar.gz" "${name}")
  fi
}

build_one windows amd64
build_one windows arm64
build_one linux amd64
build_one linux arm64
build_one darwin amd64
build_one darwin arm64

# Local convenience binary for this host
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
if [[ "$host_os" == "windows" ]]; then
  cp "dist/wikify_${VERSION}_${host_os}_${host_arch}/wikify.exe" ./wikify.exe
else
  cp "dist/wikify_${VERSION}_${host_os}_${host_arch}/wikify" ./wikify
fi

py -3 -c "
from pathlib import Path
import hashlib
dist = Path('dist')
lines = []
for p in sorted(list(dist.glob('*.zip')) + list(dist.glob('*.tar.gz'))):
    h = hashlib.sha256(p.read_bytes()).hexdigest()
    lines.append(f'{h}  {p.name}')
(dist / 'SHA256SUMS.txt').write_text('\\n'.join(lines) + '\\n', encoding='utf-8')
print((dist / 'SHA256SUMS.txt').read_text())
"

echo ""
echo "Artifacts in dist/:"
ls -lah dist/*.{zip,tar.gz,txt} 2>/dev/null || ls -lah dist/
echo "Done. VERSION=${VERSION}"
