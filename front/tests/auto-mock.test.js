import { describe, it, expect, vi } from 'vitest'
import { deriveAutoMock } from './helpers/auto-mock.js'
import * as actualWs from '../src/ws.js'
import * as actualTransportWs from '../src/platform/transport-ws/index.js'

describe('auto-mock generation (Issue #215)', () => {
  it('automatically derives all exports as mocks from actual module without drift', () => {
    const customAdmin = vi.fn().mockReturnValue(Promise.resolve({ ok: true }))
    const mocked = deriveAutoMock(actualWs, { admin: customAdmin })

    const actualKeys = Object.keys(actualWs).sort()
    const mockedKeys = Object.keys(mocked).sort()

    // Key parity guarantee: every export in actual module must exist in mock
    expect(mockedKeys).toEqual(actualKeys)

    // Override was respected
    expect(mocked.admin).toBe(customAdmin)

    // Function exports are Vitest mocks
    for (const key of actualKeys) {
      if (typeof actualWs[key] === 'function') {
        expect(vi.isMockFunction(mocked[key])).toBe(true)
      }
    }
  })

  it('preserves 1:1 export consistency between ws.js and platform/transport-ws', () => {
    const wsKeys = Object.keys(actualWs).sort()
    const transportKeys = Object.keys(actualTransportWs).sort()

    expect(wsKeys).toEqual(transportKeys)
  })

  it('derived mock works with dynamic importActual simulation', async () => {
    const actual = await vi.importActual('../src/ws.js')
    const mocked = deriveAutoMock(actual)

    expect(Object.keys(mocked).length).toBe(Object.keys(actual).length)
    expect(typeof mocked.download).toBe('function')
    expect(vi.isMockFunction(mocked.download)).toBe(true)
  })
})
