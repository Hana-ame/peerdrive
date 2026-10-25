#!/usr/bin/env bash
# scripts/build-win7.sh — 构建兼容 Windows 7 ~ 11 的 peerdrive（托盘版入口）。
#
# 方案借鉴 XTLS/Xray-core 与上游 Hana-ame/ech-proxy（scripts/build-win7.sh）：
# 用 XTLS/go-win7 补丁版 SDK 编译，产物同时兼容 Win7~11 的 386 / amd64。
#
# 为什么需要补丁 SDK：官方 Go 1.21.5+ 的运行时在启动阶段调用
# bcryptprimitives.dll!ProcessPrng，而 Win7 没有该 DLL——进程直接以
# Exception 0xc0000005 (PC=0x0 in asmstdcall) 崩在起跑线上。这就是
# 「官方最后支持 Win7 的 Go 版本是 1.21」的由来。补丁 SDK 打掉这个 API
# 依赖，back/go.mod（go 1.26.2）与整棵依赖树**完全不用动**。
#
# 用法：
#   scripts/build-win7.sh [patched-tag]     # 默认 patched-1.26.7
# 环境变量：
#   GO_WIN7_DIR   补丁 SDK 的安装目录（默认 /tmp/go-win7-sdk）
#   GO_WIN7_TAG   补丁 SDK 的 release tag（默认 patched-1.26.7）
#
# 产物：back/dist/peerdrive-win7-{386,amd64}.exe（已被 .gitignore 忽略）

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACK_DIR="$REPO_ROOT/back"
TOOLCHAIN_DIR="${GO_WIN7_DIR:-/tmp/go-win7-sdk}"
PATCHED_TAG="${1:-${GO_WIN7_TAG:-patched-1.26.7}}"

HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"   # linux / darwin
HOST_ARCH="$(uname -m)"
case "$HOST_ARCH" in
  x86_64|amd64) HOST_ARCH="amd64" ;;
  aarch64|arm64) HOST_ARCH="arm64" ;;
esac

if [ ! -x "$TOOLCHAIN_DIR/bin/go" ]; then
  echo "[1/3] Downloading XTLS/go-win7 ($PATCHED_TAG) for ${HOST_OS}-${HOST_ARCH} ..."
  mkdir -p "$TOOLCHAIN_DIR"
  ZIP_PATH="/tmp/go-for-win7-${HOST_OS}-${HOST_ARCH}.zip"
  curl -fsSL -o "$ZIP_PATH" "https://github.com/XTLS/go-win7/releases/download/${PATCHED_TAG}/go-for-win7-${HOST_OS}-${HOST_ARCH}.zip" \
    || curl -fsSL -o "$ZIP_PATH" "https://github.com/XTLS/go-win7/releases/latest/download/go-for-win7-${HOST_OS}-${HOST_ARCH}.zip"
  rm -rf "$TOOLCHAIN_DIR"/*
  unzip -q "$ZIP_PATH" -d "$TOOLCHAIN_DIR"
  GO_BIN="$(find "$TOOLCHAIN_DIR" -type f -name go -perm -u+x | head -n1)"
  if [ -z "$GO_BIN" ]; then
    echo "Error: go binary not found in archive" >&2
    exit 1
  fi
  REAL_ROOT="$(dirname "$(dirname "$GO_BIN")")"
  if [ "$REAL_ROOT" != "$TOOLCHAIN_DIR" ]; then
    mv "$REAL_ROOT"/* "$TOOLCHAIN_DIR"/
  fi
fi

# GOTOOLCHAIN=local：模块 go 指令是 1.26.2，补丁 SDK 是 1.26.7（更新），
# 必须显式锁死，否则 go 会试图按 go.mod 去下载官方工具链，白丢补丁。
export GOROOT="$TOOLCHAIN_DIR"
export PATH="$GOROOT/bin:$PATH"
export GOTOOLCHAIN=local

echo "[2/3] Using toolchain: $(go version)"
cd "$BACK_DIR"
mkdir -p dist

echo "[3/3] Building Win7~11 compatible binaries (386 / amd64, ./cmd/peerdrive-win7/) ..."
GOOS=windows GOARCH=386   CGO_ENABLED=0 go build -trimpath -buildvcs=false -tags nosqlite -ldflags="-s -w" -o dist/peerdrive-win7-386.exe   ./cmd/peerdrive-win7/
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -tags nosqlite -ldflags="-s -w" -o dist/peerdrive-win7-amd64.exe ./cmd/peerdrive-win7/

echo "Done! Output binaries in back/dist/:"
ls -lh dist/peerdrive-win7-*.exe
