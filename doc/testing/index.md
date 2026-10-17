# Testing Documentation Entry

> Entry → [README.md](README.md) (current overview) · Updated: 2026-09-20

---

| Doc/Script | Content |
|------------|---------|
| [**README.md**](README.md) | **Testing component overview**: "Changed X, which tests to run" lookup table, 14 components' commands/scales/CI mapping, blind spot list |
| [**scripts/test-layers.sh**](../../scripts/test-layers.sh) | One-click run tests layer by layer by AOP layers (L1-L8 + LB + optional INT) |
| [**Layer documentation ../layers/README.md**](../layers/README.md) | L1-L8 each layer's responsibilities, key mechanisms, tests, file lists |
| [**NetDisk manual test ../NETDISK.md §7**](../NETDISK.md#7-local-run-how-to-manually-test-these-features) | End-to-end environment setup + feature-by-feature curl checklist |
| [**v0.4.0 Manual Verification MANUAL-VERIFICATION-v0.4.0.md**](MANUAL-VERIFICATION-v0.4.0.md) | Peerdrive v0.4.0 人工测试与核心功能验证指南（遥控投屏、流切片广播、端口转发、aria2、移动端 UX） |

## Scale Snapshot (2026-09-20 actual)

| Component | Test Cases |
|-----------|------------|
| back main module unit | 308 |
| back integration (`-tags integration -p 1`) | 21 pass / 4 skip |
| back/peerjs (separate go.mod) | 23 |
| back/signalserver (separate go.mod, **no CI job**) | 21 |
| back/p2p_bt (separate go.mod, **no CI job**) | 7 |
| front vitest | 88 (9 files) |
| packages/peerdrive-client | 60 |
| packages/peerdrive-media | 21 |

## Quick Commands

```bash
bash scripts/test-layers.sh               # L1-L8 + LB layer by layer, summary at end
bash scripts/test-layers.sh --integration # Add real signaling integration segment (-p 1 serial)
cd back && go test -tags nosqlite ./...   # ⚠️ Layer script doesn't equal this full set, run both
cd front && npm test                      # Frontend 88
cd packages/peerdrive-client && npm test  # Consumer 60 (zero deps)
cd packages/peerdrive-media && npm test   # media 21
./scripts/netdisk-local-demo.sh           # NetDisk end-to-end (must run when changing netdisk path)
```

Failure logs in `/tmp/layer-test-<layer>.log`; script includes go proxy env (AGENTS.md convention).

## Archive

Old stack era testing docs (2026-04~05, libp2p / e2e-all.sh / reg-server etc.) moved to
[archive/](archive/) — historical reference only, no longer maintained, use README.md as reference.
