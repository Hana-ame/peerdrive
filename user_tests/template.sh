#!/usr/bin/env bash
# User Test Template / 用户测试模板
# 
# Exit code convention:
#   exit 0 -> PASS
#   exit 1 (or any non-zero) -> FAIL
#
# Rules:
#   - This test is formulated exclusively by the user.
#   - AI Agents MUST NOT alter or disable this test.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "==> Running user test: $(basename "$0")"

# Example assertion:
# if [ ! -f "$ROOT/Makefile" ]; then
#   echo "Error: Makefile missing!" >&2
#   exit 1
# fi

echo "==> User test $(basename "$0") passed!"
exit 0
