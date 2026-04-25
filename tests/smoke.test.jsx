import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import Sha256Manager from '../src/components/Sha256Manager'
import AnonCollectionManager from '../src/components/AnonCollectionManager'
import PathRegistrar from '../src/components/PathRegistrar'
import App from '../src/App'

describe('Sha256Manager', () => {
  it('renders without crashing', () => {
    render(<Sha256Manager />)
    expect(screen.getByText('SHA256 寻址').textContent).toBe('SHA256 寻址')
  })
})

describe('AnonCollectionManager', () => {
  it('renders without crashing (no prefill)', () => {
    render(<AnonCollectionManager />)
    expect(screen.getByText('匿名合集').textContent).toBe('匿名合集')
  })

  it('renders with prefill', () => {
    render(<AnonCollectionManager prefillEntries={[{ path: 'a.txt', hash: 'a'.repeat(64) }]} />)
    expect(screen.getByText('创建新匿名合集').textContent).toBe('创建新匿名合集')
  })
})

describe('PathRegistrar', () => {
  it('renders without crashing', () => {
    render(<PathRegistrar />)
    expect(screen.getByText('注册本地路径').textContent).toBe('注册本地路径')
  })
})

describe('App', () => {
  it('renders without crashing', () => {
    render(<App />)
    expect(screen.getByText('Peerdrive').textContent).toBe('Peerdrive')
  })
})
