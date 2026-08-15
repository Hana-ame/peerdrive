import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import Sha256Manager from '../src/components/Sha256Manager'
import P2PStatus from '../src/components/P2PStatus'
import App from '../src/App'

describe('Sha256Manager', () => {
  it('renders without crashing', () => {
    render(<Sha256Manager />)
    expect(screen.getByText('SHA256 寻址').textContent).toBe('SHA256 寻址')
  })
})

describe('P2PStatus', () => {
  it('renders without crashing', () => {
    render(<P2PStatus />)
    expect(screen.getByText('P2P 网络').textContent).toBe('P2P 网络')
  })
})

describe('App', () => {
  it('renders without crashing', () => {
    render(<App />)
    expect(screen.getByText('Peerdrive').textContent).toBe('Peerdrive')
  })
})
