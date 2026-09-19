// api-mock-sync.test.js：守卫「手写 mock 与真实 api 模块的导出同步」。
//
// 发现背景（M4）：src/__mocks__/api.js 是**手写** mock（不是 automock），新增
// api 导出后忘了同步它，页面在测试里会拿到 undefined，然后死在离原因很远的
// 调用点（"Cannot read properties of undefined"），排查成本很高。这条断言把
// 失败点前移到唯一该改的那个文件。
//
// setup.js 全局 vi.mock('../src/api.js') 会让 vitest 优先用手写 mock，所以本
// 文件先 doUnmock 再动态 import 拿真实导出清单（同 tests/api.test.js 的做法）。

import { describe, it, expect, vi } from 'vitest'

vi.doUnmock('../src/api.js')

const real = await import('../src/api.js')
const mock = await import('../src/__mocks__/api.js')

describe('src/__mocks__/api.js', () => {
  it('覆盖真实 api.js 的全部导出', () => {
    const missing = Object.keys(real).filter((k) => !(k in mock))
    expect(missing, `手写 mock 缺少导出：${missing.join(', ')}`).toEqual([])
  })

  it('不导出真实模块里不存在的东西（防改名后留下僵尸导出）', () => {
    const extra = Object.keys(mock).filter((k) => !(k in real))
    expect(extra, `手写 mock 多出导出：${extra.join(', ')}`).toEqual([])
  })
})
