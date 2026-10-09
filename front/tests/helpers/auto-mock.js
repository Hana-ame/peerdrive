import { vi } from 'vitest'

/**
 * deriveAutoMock derives a full mock implementation from actual module exports (Issue #215).
 * 
 * Instead of hand-written mocks that easily drift from the underlying API exports,
 * this function enumerates Object.keys(actual):
 *   - Any function export is mocked as vi.fn()
 *   - Non-function exports are preserved
 *   - Explicit overrides take precedence
 *
 * Discovery background (Issue #215 / §3.19): Hand-written mocks drift over time (historically
 * lost 8 exports). Deriving mocks directly from the actual module guarantees 1:1 export parity.
 */
export function deriveAutoMock(actual, overrides = {}) {
  const mocked = {}
  for (const key of Object.keys(actual)) {
    if (typeof actual[key] === 'function') {
      mocked[key] = vi.fn()
    } else {
      mocked[key] = actual[key]
    }
  }
  return { ...mocked, ...overrides }
}
