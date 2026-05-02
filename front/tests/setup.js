import { afterEach, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

// Mock the API module globally to prevent real HTTP requests during tests.
// Components call API functions in useEffect, which would create real Node
// HTTP requests in happy-dom, causing AbortError noise during teardown.
vi.mock('../src/api.js')

afterEach(() => {
  cleanup()
})
