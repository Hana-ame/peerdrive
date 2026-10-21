import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import React from 'react'
import ContextMenu from '../src/components/netdisk/ContextMenu'

// context_menu.test.jsx — Context menu interaction and viewport bound tests (Issue #252).
// 发现背景：Issue #252（网盘文件右键自定义上下文菜单，支持预览、下载、投屏、分享与快捷操作）。
describe('ContextMenu Component', () => {
  it('renders menu items and triggers actions', () => {
    const onAction = vi.fn()
    const onClose = vi.fn()
    const items = [
      { label: '预览', icon: '👁️', onClick: onAction },
      { divider: true },
      { label: '删除', icon: '🗑️', danger: true, onClick: onAction },
    ]

    render(<ContextMenu x={100} y={150} items={items} onClose={onClose} />)

    const menu = screen.getByTestId('drive-context-menu')
    expect(menu).toBeInTheDocument()

    const previewBtn = screen.getByText('预览')
    expect(previewBtn).toBeInTheDocument()

    fireEvent.click(previewBtn)
    expect(onAction).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes on Escape key press', () => {
    const onClose = vi.fn()
    render(<ContextMenu x={50} y={50} items={[{ label: '测试' }]} onClose={onClose} />)

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('closes on outside click', () => {
    const onClose = vi.fn()
    render(
      <div>
        <div data-testid="outside">Outside area</div>
        <ContextMenu x={50} y={50} items={[{ label: '测试' }]} onClose={onClose} />
      </div>
    )

    fireEvent.pointerDown(screen.getByTestId('outside'))
    expect(onClose).toHaveBeenCalled()
  })
})
