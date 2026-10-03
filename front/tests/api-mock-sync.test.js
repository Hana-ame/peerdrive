// api-mock-sync.test.js: guards "hand-written mock stays in sync with the real api module's exports".
//
// Discovery context (M4): src/__mocks__/api.js is a **hand-written** mock (not automock). After
// adding a new api export, forgetting to sync it means the page gets undefined in tests and
// then dies at a call site far from the root cause ("Cannot read properties of undefined"),
// making debugging very expensive. This assertion moves the failure point to the one file
// that actually needs to change.
//
// setup.js does a global vi.mock('../src/api.js'), making vitest prefer the hand-written mock,
// so this file first doUnmocks, then dynamically imports to get the real export list
// (same approach as tests/api.test.js).

import { describe, it, expect, vi } from 'vitest'

vi.doUnmock('../src/api.js')

const real = await import('../src/api.js')
const mock = await import('../src/__mocks__/api.js')

describe('src/__mocks__/api.js', () => {
  it('covers all exports from the real api.js', () => {
    const missing = Object.keys(real).filter((k) => !(k in mock))
    expect(missing, `Hand-written mock is missing exports: ${missing.join(', ')}`).toEqual([])
  })

  it('does not export anything not present in the real module (prevents zombie exports after renames)', () => {
    const extra = Object.keys(mock).filter((k) => !(k in real))
    expect(extra, `Hand-written mock has extra exports: ${extra.join(', ')}`).toEqual([])
  })
})
