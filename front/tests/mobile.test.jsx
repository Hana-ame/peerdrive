import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import App from '../src/App'

// mobile.test.jsx — Mobile responsiveness & UX tests (Issue #235).
// 发现背景：Issue #235（手机移动端网页 UX 适配与体验优化：抽屉式菜单、底部导航、安全区样式与触控支持）。
describe('Mobile UX & Navigation', () => {
  it('renders mobile menu button and bottom bar', () => {
    render(<App />)
    const mobileButton = screen.getByTestId('mobile-menu-button')
    expect(mobileButton).toBeInTheDocument()

    const bottomBar = screen.getByTestId('mobile-bottom-bar')
    expect(bottomBar).toBeInTheDocument()
  })

  it('opens and closes mobile drawer upon user interaction', () => {
    render(<App />)
    const mobileButton = screen.getByTestId('mobile-menu-button')
    
    // Drawer starts closed
    expect(screen.queryByTestId('mobile-drawer-backdrop')).not.toBeInTheDocument()

    // Click hamburger to open drawer
    fireEvent.click(mobileButton)
    const backdrop = screen.getByTestId('mobile-drawer-backdrop')
    expect(backdrop).toBeInTheDocument()

    // Click backdrop to close
    fireEvent.click(backdrop)
    expect(screen.queryByTestId('mobile-drawer-backdrop')).not.toBeInTheDocument()
  })
})
