#!/bin/sh
set -eu
cd "$(dirname "$0")"
arch=$(uname -m)
case "$arch" in x86_64) bin=studio_darwin_amd64 ;; arm64) bin=studio_darwin_arm64 ;; *) echo "Unsupported macOS CPU: $arch"; exit 1 ;; esac
chmod +x "./$bin" "./core/alloc_darwin_${bin##*_}" 2>/dev/null || true
echo "Opening Allocation Studio: http://127.0.0.1:8866/designer"
exec "./$bin"
