#!/bin/sh
# ASCII-only launcher. Native engine requires no Go/Python/Java installation.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SYS=$(uname -s)
ARCH=$(uname -m)
case "$SYS/$ARCH" in
  Linux/x86_64|Linux/amd64) BIN=alloc_linux_amd64 ;;
  Linux/aarch64|Linux/arm64) BIN=alloc_linux_arm64 ;;
  Darwin/x86_64) BIN=alloc_darwin_amd64 ;;
  Darwin/arm64) BIN=alloc_darwin_arm64 ;;
  *) echo "Unsupported OS/CPU: $SYS/$ARCH; consult PLATFORM_SUPPORT_CN.md" >&2; exit 2 ;;
esac
ENGINE="$ROOT/core/$BIN"
if [ ! -f "$ENGINE" ]; then echo "Missing native binary $ENGINE" >&2; exit 2; fi
chmod u+x "$ENGINE" || { echo "Cannot set executable bit: $ENGINE" >&2; exit 2; }
MODE=${1:-smoke}
if [ "$#" -gt 0 ]; then shift; fi
case "$MODE" in
  smoke)
    mkdir -p "$ROOT/runs"
    OUT="$ROOT/runs/native_smoke_$(date +%Y%m%d_%H%M%S)_$$"
    echo "Native OS: $SYS | CPU: $ARCH | Executable: $BIN"
    "$ENGINE" doctor "$ROOT/core/examples/manufacturing/rule.json"
    "$ENGINE" run "$ROOT/core/examples/manufacturing/rule.json" "$OUT" --workers 2 --evidence-gzip
    echo "PASS; result directory: $OUT"
    ;;
  serve)
    exec "$ENGINE" serve --root "$ROOT/core/examples" --store "$ROOT/service_data" --listen 127.0.0.1:8787 --jobs 2
    ;;
  help|--help|-h) exec "$ENGINE" --help ;;
  acceptance)
    if command -v python3 >/dev/null 2>&1; then
      exec python3 "$ROOT/acceptance.py" --mode all --workspace "$ROOT/acceptance_runs" "$@"
    fi
    echo 'Python3 needed ONLY for developer acceptance tool; engine runs without it.' >&2
    exit 2
    ;;
  *) exec "$ENGINE" "$MODE" "$@" ;;
esac
