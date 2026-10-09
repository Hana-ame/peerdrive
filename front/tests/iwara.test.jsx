// Iwara.jsx tests — the BBS-post-styled iwara viewer.
//
// Discovery context (2026-10, iwara page): the page's contract is "one iwara
// video renders as one BBS post" — left author column, tiptop floor badge +
// op menu + font switch, h1 title, 14px body with a capped cover image and a
// resolution table whose rows are real CDN download links. The risks pinned
// here are: the layout must actually carry the iwara data (a post that looks
// right but drops the resolution table is useless), the download link must be
// the https CDN URL the backend signed (not a placeholder), and when the node
// has no iwara module the page must degrade to the demo entry with an honest
// message instead of rendering a fake video as if it were real. ws is mocked
// entirely — no network, and the demo entry loads no external image.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'

const { adminMock } = vi.hoisted(() => ({ adminMock: vi.fn() }))
vi.mock('../src/ws.js', () => ({
  admin: adminMock,
}))

import Iwara, { fmtDuration, fmtCount, fmtDate, iwaraUrl } from '../src/pages/Iwara'

// A resolved metadata payload, shaped exactly as controller.GetIwaraVideo returns it.
const video = (over = {}) => ({
  id: 'ab12cd34',
  title: '【IwaraTool】I站下载器 v2.1 发布',
  status: 'published',
  rating: 'general',
  author: 'IwaraTool',
  authorId: 'u-7788',
  cover: 'https://img.iwara.tv/thumb/ab12cd34.jpg',
  duration: 182,
  views: 12800,
  uploaded: 1728000000,
  file: { id: 'f-1', name: 'iwara-tool-v2.1.mp4' },
  resolutions: [
    { id: 'src', name: 'Source', downloadUrl: 'https://v-f007.v.ihstatic.com/src.mp4?token=t1' },
    { id: 'hi', name: '1080p', downloadUrl: 'https://v-f007.v.ihstatic.com/hi.mp4?token=t2' },
  ],
  ...over,
})

function renderAt(path) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/iwara" element={<Iwara />} />
        <Route path="/iwara/:id" element={<Iwara />} />
      </Routes>
    </MemoryRouter>,
  )
}

async function flush(times = 8) {
  for (let i = 0; i < times; i++) await act(async () => { await Promise.resolve() })
}

// parseIn: type ids into the input box and click 解析, then let the promise settle.
// Every test that needs real (non-demo) data goes through this path, because the
// page ships with the demo entry and only replaces it with a real response.
async function parseIn(values, mockResults) {
  // 用 mockResolvedValueOnce / mockRejectedValueOnce（懒创建），不要用默认的
  // mockRejectedValue：vitest 在调用 mockRejectedValue 那一刻就 eager 地构造
  // Promise.reject(...)，页面 await 到它之前的那个 tick 已构成 unhandled
  // rejection，用例会被判失败（返回值渲染是正常的，纯属计时器问题）。
  const results = Array.isArray(mockResults) ? mockResults : [mockResults]
  for (const r of results) {
    if (r instanceof Error) adminMock.mockRejectedValueOnce(r)
    else adminMock.mockResolvedValueOnce(r)
  }
  renderAt('/iwara')
  fireEvent.change(screen.getByLabelText(/iwara URL or video id/i), { target: { value: values } })
  fireEvent.click(screen.getByText('解析'))
  await flush()
}

describe('format helpers', () => {
  it('fmtDuration renders m:ss / h:mm:ss and a placeholder for unknown', () => {
    expect(fmtDuration(182)).toBe('3:02')
    expect(fmtDuration(3671)).toBe('1:01:11')
    expect(fmtDuration(0)).toBe('—')
    expect(fmtDuration(null)).toBe('—')
  })

  it('fmtCount abbreviates large view counts and shows a placeholder for none', () => {
    expect(fmtCount(950)).toBe('950')
    expect(fmtCount(12800)).toBe('12.8k')
    expect(fmtCount(1200000)).toBe('1.2M')
    expect(fmtCount(0)).toBe('—')
  })

  it('fmtDate renders unix seconds and a placeholder for 0', () => {
    // 1728000000 is a fixed point in time; assert the shape, not the zone.
    expect(fmtDate(1728000000)).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
    expect(fmtDate(0)).toBe('—')
  })

  it('iwaraUrl builds the canonical deep link', () => {
    expect(iwaraUrl({ id: 'ab12cd34' })).toBe('https://www.iwara.tv/videos/ab12cd34')
  })
})

describe('BBS post layout + iwara data', () => {
  beforeEach(() => adminMock.mockReset())

  it('renders the post skeleton: author column, tiptop, h1 title, body, bottom action bar', async () => {
    await parseIn('ab12cd34', video())
    // 左栏作者信息区
    expect(screen.getByText('IwaraTool')).toBeTruthy()
    expect(screen.getByText('UID')).toBeTruthy()
    // tiptop 楼层徽标 + 操作下拉 + 字号切换
    expect(screen.getByText(/楼层 1/)).toBeTruthy()
    expect(screen.getByText('楼主')).toBeTruthy()
    expect(screen.getByText('操作 ▾')).toBeTruthy()
    expect(screen.getByText('A-')).toBeTruthy()
    expect(screen.getByText('A+')).toBeTruthy()
    // h1 标题区
    expect(screen.getByRole('heading', { level: 1, name: /I站下载器/ })).toBeTruthy()
    // 底部操作条
    expect(screen.getByText('↑ 顶端')).toBeTruthy()
    expect(screen.getByText(/↩ 回复/)).toBeTruthy()
    expect(screen.getByText(/❞ 引用/)).toBeTruthy()
  })

  it('renders the iwara entry data: author stats, duration, views, resolution options, download link', async () => {
    await parseIn('ab12cd34', video())
    // 左栏竖排统计（uid / 精华 / 观看 / 时长）
    expect(screen.getByText('u-7788')).toBeTruthy()
    expect(screen.getByText('general')).toBeTruthy()
    expect(screen.getByText('3:02')).toBeTruthy()
    expect(screen.getByText('12.8k')).toBeTruthy()
    // 分辨率选项行
    expect(screen.getByText('Source')).toBeTruthy()
    expect(screen.getByText('1080p')).toBeTruthy()
    // 下载入口：真实的 CDN URL，不是占位符
    // 每个清晰度各有一个下载链接
    const links = screen.getAllByRole('link', { name: /下载/ })
    expect(links).toHaveLength(2)
    expect(links[0].getAttribute('href')).toBe('https://v-f007.v.ihstatic.com/src.mp4?token=t1')
    expect(links[1].getAttribute('href')).toBe('https://v-f007.v.ihstatic.com/hi.mp4?token=t2')
    expect(links[0].getAttribute('download')).not.toBeNull()
    // 入口链接回到 iwara 原页面
    expect(screen.getByRole('link', { name: '打开 Iwara 原页面' }).getAttribute('href'))
      .toBe('https://www.iwara.tv/videos/ab12cd34')
    // 源文件名（卖点段落和底部各出现一次）+ 分辨率计数
    expect(screen.getAllByText(/iwara-tool-v2\.1\.mp4/).length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText(/2 个下载入口/)).toBeTruthy()
  })

  it('renders a cover image when the backend returns one', async () => {
    await parseIn('ab12cd34', video())
    const img = screen.getByAltText(/cover of/)
    expect(img.getAttribute('src')).toBe('https://img.iwara.tv/thumb/ab12cd34.jpg')
  })

  it('the demo entry shows no fake download link and no external image', () => {
    renderAt('/iwara')
    // demo entry: downloadUrl 为空 → 不存在可点的下载链接
    expect(screen.queryByRole('link', { name: /下载/ })).toBeNull()
    expect(screen.getByText('示例条目 · 无封面')).toBeTruthy()
    // 页面明确标注这是演示数据
    expect(screen.getByText(/示例条目为演示数据/)).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })
})

describe('interactions', () => {
  beforeEach(() => adminMock.mockReset())

  it('the font switch changes the post body font size', () => {
    renderAt('/iwara')
    // h1 与 .posttext 是同级兄弟，不是祖孙关系，所以取父节点再向下找
    const posttext = () =>
      screen.getByRole('heading', { level: 1, name: /版式示例条目/ })
        .parentElement.querySelector('.posttext')
    // 默认 14px（.f14 原版正文）
    expect(posttext().style.fontSize).toBe('14px')
    fireEvent.click(screen.getByText('A+'))
    expect(posttext().style.fontSize).toBe('16px')
    fireEvent.click(screen.getByText('A-'))
    expect(posttext().style.fontSize).toBe('12px')
  })

  it('只看楼主 keeps only the anchored author, 屏蔽 hides that author', async () => {
    await parseIn('ab12cd34 ee55ff66', [
      video(),
      video({ id: 'ee55ff66', title: '另一位作者的作品', author: 'OtherUP', authorId: 'u-2' }),
    ])
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(2)

    // 只看楼主（第一个帖子的作者）→ 只剩一个帖子
    fireEvent.click(screen.getAllByRole('button', { name: '操作 ▾' })[0])
    fireEvent.click(screen.getAllByRole('menuitem', { name: '只看楼主' })[0])
    await flush()
    let headings = screen.getAllByRole('heading', { level: 1 })
    expect(headings).toHaveLength(1)
    expect(headings[0].textContent).toContain('I站下载器')
    expect(screen.getByText(/只看 OtherUP|只看 IwaraTool/)).toBeTruthy()

    // 取消只看，再屏蔽同一作者 → 只剩另一个作者
    fireEvent.click(screen.getByText('×'))
    await flush()
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(2)
    fireEvent.click(screen.getAllByRole('button', { name: '操作 ▾' })[0])
    fireEvent.click(screen.getAllByRole('menuitem', { name: /屏蔽/ })[0])
    await flush()
    headings = screen.getAllByRole('heading', { level: 1 })
    expect(headings).toHaveLength(1)
    expect(headings[0].textContent).toContain('另一位作者')
  })

  it('回复 and 引用 copy BBS-style text to the clipboard', async () => {
    const writeMock = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: writeMock }, configurable: true })

    renderAt('/iwara')
    fireEvent.click(screen.getByText('↑ 顶端')) // also verifies the button is wired
    fireEvent.click(screen.getByText(/↩ 回复/))
    await flush()
    expect(writeMock).toHaveBeenLastCalledWith(expect.stringContaining('回复 IwaraTool 的《'))

    fireEvent.click(screen.getByText(/❞ 引用/))
    await flush()
    const last = writeMock.mock.calls[writeMock.mock.calls.length - 1][0]
    expect(last).toContain('> 【IwaraTool】')
    expect(last).toContain('https://www.iwara.tv/videos/demo-0001')
  })
})

describe('data source', () => {
  beforeEach(() => adminMock.mockReset())

  it('fetches each pasted id over the admin channel and drops the demo entry', async () => {
    await parseIn('ab12cd34 ee55ff66', [
      video(),
      video({ id: 'ee55ff66', title: '另一个视频', author: 'AnotherUP' }),
    ])
    expect(adminMock).toHaveBeenCalledTimes(2)
    // ws.admin(method, path) — body 省略时默认参数不进入调用记录
    expect(adminMock.mock.calls[0]).toEqual(['GET', '/iwara/video/ab12cd34'])
    expect(adminMock.mock.calls[1]).toEqual(['GET', '/iwara/video/ee55ff66'])
    // 两个 id 都返回同一个 mock 值，所以是 2 条
    expect(screen.getByText(/已解析 2 条/)).toBeTruthy()
    // demo 条目被真实数据替换
    expect(screen.queryByRole('heading', { level: 1, name: /版式示例条目/ })).toBeNull()
  })

  it('reports partial failure when only some ids resolve', async () => {
    const err = Object.assign(new Error('iwara: video "gone" not found'), {
      status: 404, data: { error: 'iwara: video "gone" not found' },
    })
    await parseIn('ab12cd34 gone', [video(), err])
    expect(screen.getByText(/已解析 1 条，1 条失败/)).toBeTruthy()
  })

  it('a deep link /iwara/:id parses that id on mount', async () => {
    adminMock.mockResolvedValue(video())
    renderAt('/iwara/zz99yy88')
    await flush()
    expect(adminMock).toHaveBeenCalledTimes(1)
    expect(adminMock.mock.calls[0][1]).toBe('/iwara/video/zz99yy88')
  })

  it('accepts a full iwara URL as the id', async () => {
    await parseIn('https://www.iwara.tv/videos/ab12cd34', video())
    // encodeURIComponent 后的路径透传给后端，由 ParseVideoID 归一化
    expect(adminMock.mock.calls[0][1])
      .toBe('/iwara/video/' + encodeURIComponent('https://www.iwara.tv/videos/ab12cd34'))
  })

  it('falls back to the demo entry with an honest message when the route is missing', async () => {
    // gin 的未注册路由返回字符串体 "404 page not found"（不是 JSON）
    await parseIn('ab12cd34', Object.assign(new Error('HTTP 404'), { status: 404, data: '404 page not found' }))
    expect(screen.getByText(/未启用 iwara 模块/)).toBeTruthy()
    // 版式仍然可见
    expect(screen.getByRole('heading', { level: 1, name: /版式示例条目/ })).toBeTruthy()
  })

  it('shows the backend error when the module is on but the video cannot be resolved', async () => {
    await parseIn('gone', Object.assign(new Error('iwara: video "gone" not found'), {
      status: 404, data: { error: 'iwara: video "gone" not found' },
    }))
    expect(screen.getByText(/not found/)).toBeTruthy()
    // 不假装成功：错误可见，示例条目仍在（版式可看）
    expect(screen.getByRole('heading', { level: 1, name: /版式示例条目/ })).toBeTruthy()
  })

  it('tells the user to connect when the ws session is offline', async () => {
    await parseIn('ab12cd34', new Error('ws: not connected'))
    expect(screen.getByText(/未连接到本地节点/)).toBeTruthy()
  })

  it('rejects an empty input without calling the backend', async () => {
    adminMock.mockResolvedValue(video())
    renderAt('/iwara')
    fireEvent.change(screen.getByLabelText(/iwara URL or video id/i), { target: { value: '   ' } })
    // 提交按钮在空输入时是 disabled 的
    const submit = screen.getByRole('button', { name: /解析/ })
    expect(submit.disabled).toBe(true)
    await flush()
    expect(adminMock).not.toHaveBeenCalled()
  })

  it('the 示例条目 button reloads the demo entry and clears a previous error', async () => {
    await parseIn('ab12cd34', Object.assign(new Error('HTTP 404'), { status: 404, data: '404 page not found' }))
    expect(screen.getByText(/未启用 iwara 模块/)).toBeTruthy()
    fireEvent.click(screen.getByText('示例条目'))
    await flush()
    expect(screen.queryByText(/未启用 iwara 模块/)).toBeNull()
    expect(screen.getByText(/示例条目（演示数据/)).toBeTruthy()
  })
})
