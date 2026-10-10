import { afterEach, expect } from 'vitest'
import { cleanup } from '@testing-library/react'
import * as matchers from '@testing-library/jest-dom/matchers'

expect.extend(matchers)

// 2026-10-07: the global `vi.mock('../src/api.js')` was removed together with
// src/api.js. That module was a zombie: no page or component imported it (all
// traffic goes through ws.js admin frames), so the mock only existed to keep a
// dead file from issuing real HTTP requests during tests. Nothing in the app
// opens a connection at import time — ws.js connects lazily on connect() — so
// no equivalent guard is needed here. If a test ever needs to stub the WS
// layer, it should mock '../src/ws.js' in that test file instead.

afterEach(() => {
  cleanup()
})

if (typeof window !== 'undefined') {
  class MockIntersectionObserver {
    constructor(callback) {
      this.callback = callback
    }
    observe(target) {
      this.callback([{ isIntersecting: true, target }])
    }
    unobserve() {}
    disconnect() {}
  }
  window.IntersectionObserver = MockIntersectionObserver
  global.IntersectionObserver = MockIntersectionObserver
}