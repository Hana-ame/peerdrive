import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import Sha256Manager from '../src/components/Sha256Manager'
import CollectionBuilder from '../src/components/CollectionBuilder'
import App from '../src/App'

describe('Sha256Manager', () => {
  it('renders without crashing', () => {
    render(<Sha256Manager />)
    expect(screen.getByText('SHA256 寻址').textContent).toBe('SHA256 寻址')
  })
})

describe('CollectionBuilder', () => {
  it('renders without crashing', () => {
    render(<CollectionBuilder />)
    expect(screen.getByText('合集构建器').textContent).toBe('合集构建器')
    expect(screen.getByText('浏览合集').textContent).toBe('浏览合集')
  })
})

describe('App', () => {
  it('renders without crashing', () => {
    render(<App />)
    expect(screen.getByText('Peerdrive').textContent).toBe('Peerdrive')
  })
})
