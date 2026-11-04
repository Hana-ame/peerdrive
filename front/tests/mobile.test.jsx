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

  it('hides top navbar LanguageSelector on small screens and keeps it inside drawer (Issue #344)', () => {
    const { container } = render(<App />)
    const topLangWrapper = container.querySelector('nav .hidden.sm\\:flex')
    expect(topLangWrapper).toBeInTheDocument()

    // Open drawer and ensure language selector is present for mobile users
    const mobileButton = screen.getByTestId('mobile-menu-button')
    fireEvent.click(mobileButton)
    const drawer = screen.getByTestId('mobile-drawer-backdrop')
    expect(drawer).toBeInTheDocument()
    const selectors = screen.getAllByTestId('language-selector')
    expect(selectors.length).toBeGreaterThanOrEqual(2) // 1 in top nav, 1 in drawer
  })

  it('collapses status pills to dot-only indicators on mobile (< md) (Issue #342, #344)', () => {
    render(<App />)
    const signalPill = screen.getByTestId('signal-status')
    const connectionPill = screen.getByTestId('connection-status')

    expect(signalPill.className).toContain('p-1.5')
    expect(signalPill.className).toContain('md:px-2.5')
    expect(connectionPill.className).toContain('p-1.5')
    expect(connectionPill.className).toContain('md:px-2.5')

    const signalText = signalPill.querySelector('.hidden.md\\:inline')
    expect(signalText).toBeInTheDocument()

    const connectionText = connectionPill.querySelector('.hidden.md\\:inline')
    expect(connectionText).toBeInTheDocument()
  })

  it('app root container enforces overflow-x-hidden and max-w-full to prevent mobile blowout (Issue #344)', () => {
    const { container } = render(<App />)
    const appWrapper = container.querySelector('.flex.flex-col.h-screen')
    expect(appWrapper).toBeInTheDocument()
    expect(appWrapper.className).toContain('overflow-x-hidden')
    expect(appWrapper.className).toContain('max-w-full')
  })
})
