// shared.test.js — platform/shared module contracts and barrel re-exports.
//
// 发现背景：Issue #63 将 format、swBridge、collectionTree 收进 platform/shared，
// 本测试作为守护网，确保 shared 统一导出与向后兼容门面行为完全一致。

import { describe, it, expect } from 'vitest'
import * as shared from '../src/platform/shared'
import * as formatOriginal from '../src/lib/format'
import * as swBridgeOriginal from '../src/lib/swBridge'
import * as collectionTreeOriginal from '../src/features/collection/lib/collectionTree'

describe('platform/shared exports and compatibility', () => {
  it('exports format helpers and matches lib/format facade', () => {
    // 发现背景：format.js 移入 platform/shared 后，旧路径 lib/format.js 必须保持逐函数引用等价
    expect(shared.fmtBytes).toBe(formatOriginal.fmtBytes)
    expect(shared.fmtRate).toBe(formatOriginal.fmtRate)
    expect(shared.fmtEta).toBe(formatOriginal.fmtEta)
    expect(shared.progressPct).toBe(formatOriginal.progressPct)
    expect(shared.RateTracker).toBe(formatOriginal.RateTracker)
    expect(shared.estEtaSeconds).toBe(formatOriginal.estEtaSeconds)

    expect(shared.fmtBytes(1500)).toBe('1.50 KB')
    expect(shared.fmtRate(2000000)).toBe('2.00 MB/s')
    expect(shared.fmtEta(65)).toBe('1m 5s')
  })

  it('exports swBridge helpers and matches lib/swBridge facade', () => {
    // 发现背景：swBridge 移入 platform/shared 后，registerSW 与 swControlled 需保持等价导出
    expect(shared.registerSW).toBe(swBridgeOriginal.registerSW)
    expect(shared.swControlled).toBe(swBridgeOriginal.swControlled)
  })

  it('exports collectionTree helpers and matches original feature facade', () => {
    // 发现背景：collectionTree 移入 platform/shared 后，目录树构建纯函数需保持等价导出
    expect(shared.buildTree).toBe(collectionTreeOriginal.buildTree)
    expect(shared.descend).toBe(collectionTreeOriginal.descend)
    expect(shared.basename).toBe(collectionTreeOriginal.basename)
    expect(shared.entrySha).toBe(collectionTreeOriginal.entrySha)
    expect(shared.previewSha).toBe(collectionTreeOriginal.previewSha)
    expect(shared.normalizeEntry).toBe(collectionTreeOriginal.normalizeEntry)
    expect(shared.splitPath).toBe(collectionTreeOriginal.splitPath)

    const tree = shared.buildTree([
      { path: 'docs/guide.md', sha: 'a'.repeat(64) },
      { path: 'images/logo.png', sha: 'b'.repeat(64) },
    ])
    expect(tree.type).toBe('dir')
    expect(tree.children.length).toBe(2)
  })
})
