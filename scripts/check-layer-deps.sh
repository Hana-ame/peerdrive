#!/usr/bin/env bash
# check-layer-deps.sh — 依赖图边界守卫（doc/LAYERS.md §2, §6 & doc/REFACTOR.md §8）。
#
# 验证硬性分层约束：
#   1. internal/service 严禁直接 import internal/transport（§8 规则 3，防跨层耦合）；
#   2. internal/service 严禁反向 import internal/controller（防倒灌）；
#   3. internal/transport 严禁 import internal/service 或 internal/controller（LAYERS.md §6）；
#   4. internal/repository 严禁 import service, controller, transport；
#   5. peerjs 独立模块严禁 import internal/*。
#
# 用法:
#   bash scripts/check-layer-deps.sh
# 退出码:
#   0: 全部依赖方向合规
#   1: 发现越层依赖

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACK="$ROOT/back"

FAIL=0
RED='\033[31m'
GREEN='\033[32m'
NC='\033[0m'

check_no_import() {
  local pkg="$1"
  local forbidden="$2"
  local reason="$3"

  local imports
  imports=$(cd "$BACK" && go list -f '{{range .Imports}}{{println .}}{{end}}' "$pkg" 2>/dev/null || true)

  if echo "$imports" | grep -E "^${forbidden}$" >/dev/null 2>&1; then
    echo -e "${RED}[VIOLATION]${NC} Package '$pkg' imports forbidden dependency '$forbidden'!"
    echo "  Reason: $reason"
    FAIL=1
  fi
}

echo "== Checking Architectural Layer Dependencies =="

# 1. service 严禁 import transport (REFACTOR §8 Rule 3)
check_no_import "peerdrive/internal/service" "peerdrive/internal/transport" \
  "service must not directly import transport; wiring must happen in assembly (serverapp/app.go) via interfaces/callbacks"

# 2. service 严禁 import controller (单向分层)
check_no_import "peerdrive/internal/service" "peerdrive/internal/controller" \
  "service must not import controller; dependency direction is controller -> service"

# 3. transport 严禁 import service / controller (LAYERS §6 Checklist)
check_no_import "peerdrive/internal/transport" "peerdrive/internal/service" \
  "transport must not import service; callbacks and providers must be injected from outside"
check_no_import "peerdrive/internal/transport" "peerdrive/internal/controller" \
  "transport must not import controller; admin multiplexing goes through Gin engine interface"

# 4. repository 严禁 import 高层业务/传输包
check_no_import "peerdrive/internal/repository" "peerdrive/internal/service" \
  "repository is L5 data persistence and must not import service"
check_no_import "peerdrive/internal/repository" "peerdrive/internal/controller" \
  "repository is L5 data persistence and must not import controller"
check_no_import "peerdrive/internal/repository" "peerdrive/internal/transport" \
  "repository is L5 data persistence and must not import transport"

# 5. peerjs 独立模块严禁 import 主程序 internal 包
PEERJS_IMPORTS=$(cd "$BACK/peerjs" && go list -f '{{range .Imports}}{{println .}}{{end}}' ./... 2>/dev/null || true)
if echo "$PEERJS_IMPORTS" | grep -E "peerdrive/internal" >/dev/null 2>&1; then
  echo -e "${RED}[VIOLATION]${NC} peerjs module imports peerdrive/internal!"
  echo "  Reason: peerjs is an independent L1 transport primitive module and must have 0 internal dependencies"
  FAIL=1
fi

if [ "$FAIL" -eq 0 ]; then
  echo -e "${GREEN}✓ All layer dependency rules satisfied (0 violations).${NC}"
  exit 0
else
  echo -e "${RED}✗ Layer dependency check failed.${NC}"
  exit 1
fi
