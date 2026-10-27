import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import React from 'react'
import ErrorBoundary from '../src/components/ErrorBoundary'

function ProblemChild({ shouldThrow }) {
  if (shouldThrow) {
    throw new Error('Test crash')
  }
  return <div>Normal Content</div>
}

describe('ErrorBoundary', () => {
  it('renders children when no error occurs', () => {
    render(
      <ErrorBoundary>
        <ProblemChild shouldThrow={false} />
      </ErrorBoundary>
    )
    expect(screen.getByText('Normal Content')).toBeDefined()
  })

  it('renders default fallback UI on crash and supports in-situ reset', () => {
    // Suppress console.error in test output for intentional throw
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})

    let throwFlag = true
    function DynamicChild() {
      if (throwFlag) throw new Error('Exploded')
      return <div>Recovered Content</div>
    }

    const { rerender } = render(
      <ErrorBoundary>
        <DynamicChild />
      </ErrorBoundary>
    )

    expect(screen.getByText('页面渲染出错了')).toBeDefined()
    expect(screen.getByText('重试 · Try Again')).toBeDefined()

    // Flip flag and click retry
    throwFlag = false
    fireEvent.click(screen.getByText('重试 · Try Again'))

    rerender(
      <ErrorBoundary>
        <DynamicChild />
      </ErrorBoundary>
    )
    expect(screen.getByText('Recovered Content')).toBeDefined()

    spy.mockRestore()
  })

  it('supports custom fallback render prop', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})

    render(
      <ErrorBoundary fallback={({ error, resetError }) => (
        <div>
          <span>Custom Error: {error?.message}</span>
          <button onClick={resetError}>Reset</button>
        </div>
      )}>
        <ProblemChild shouldThrow={true} />
      </ErrorBoundary>
    )

    expect(screen.getByText('Custom Error: Test crash')).toBeDefined()
    spy.mockRestore()
  })
})
