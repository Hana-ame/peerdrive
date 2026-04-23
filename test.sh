#!/bin/bash

API_BASE="${API_BASE:-http://localhost:3000}"

echo "=== Peerdrive 测试脚本 ==="
echo "API_BASE: $API_BASE"
echo ""

echo "1. 测试 /ping"
curl -x "" -s "$API_BASE/ping"
echo ""
echo ""

echo "2. 测试 /p2p/node"
curl -x "" -s "$API_BASE/p2p/node"
echo ""
echo ""

echo "3. 测试 /p2p/peers"
curl -x "" -s "$API_BASE/p2p/peers"
echo ""
echo ""

echo "=== 测试完成 ==="