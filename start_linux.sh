#!/bin/sh
set -eu
cd "$(dirname "$0")"
arch=$(uname -m)
case "$arch" in x86_64|amd64) bin=studio_linux_amd64 ;; aarch64|arm64) bin=studio_linux_arm64 ;; *) echo "Unsupported Linux CPU: $arch"; exit 1 ;; esac
chmod +x "./$bin" "./core/alloc_linux_${bin##*_}" 2>/dev/null || true
echo "Opening Allocation Studio: http://127.0.0.1:8866/designer"
exec "./$bin"
