// Iwara.jsx tests — the netdisk-style iwara browser.
//
// Discovery context (2026-10, iwara page): the first cut of this page rendered
// each video as a BBS forum post (author sidebar, floor badges, "only show OP",
// font-size toggles, reply/quote actions) and the page's contract was written
// around that shape. That reference was wrong — this is a content browser, so
// the page now follows Drive.jsx / CollectionView.jsx: a header with a
// grid/list toggle, an input bar, then either preview cards or one table of
// rows, each carrying cover + title + author + duration + views and a per-row
// quality select + download / copy-link / open-source action group.
//
// What the tests pin now: the layout must carry the iwara data (a card or row
// that looks right but drops the resolution data is useless), the download link
// must be the https CDN URL the backend signed rather than a placeholder, the
// quality select must actually change which URL is downloaded, and when the
// node has no iwara module the page must fall back to labelled sample data with
// an honest message instead of rendering a fake video as if it were real.
// ws is mocked entirely — no network, and the sample entry loads no image.
//
// Vitest gotcha kept from the previous revision: a mock set up with
// mockRejectedValue / mockImplementation(() => Promise.reject(e)) eagerly
// creates Promise.reject(e) at setup time, so the tick before the page awaits
// it is already an unhandled rejection and the test fails with the bare error
// even though the page catches and renders it. mockRejectedValueOnce is used
// per result instead, which defers creation until the call happens.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'

const { adminMock } = vi.hoisted(() => ({ adminMock: vi.fn() }))
vi.mock('../src/ws.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    admin: adminMock,
  }
})
vi.mock('../src/platform/transport-ws', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    admin: adminMock,
  }
})

import Iwara, { fmtDuration, fmtCount, fmtDate, iwaraUrl, parseIds } from '../src/pages/Iwara'

// Shaped exactly as controller.GetIwaraVideo returns it (echproxy.VideoMeta).
const video = (over = {}) => ({
  id: 'ab12cd34',
  title: 'iwara tool v2.1 release',
  status: 'published',
  rating: 'general',
  author: 'IwaraTool',
  authorId: 'u-7788',
  cover: 'https://img.iwara.tv/thumb/ab12cd34.jpg',
  duration: 182,
  views: 12800,
  uploaded: 1728000000,
  resolutions: [
    { id: 'src', name: 'Source', downloadUrl: 'https://v-f007.v.ihstatic.com/src.mp4?token=t1' },
    { id: 'hi', name: '1080p', downloadUrl: 'https://v-f007.v.ihstatic.com/hi.mp4?token=t2' },
  ],
  ...over,
})

// ws.admin rejects with the backend body attached as .data.
const wsErr = (msg, data) => {
  const e = new Error(msg)
  e.data = data
  return e
}

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

// parentWalk: nearest ancestor matching fn. The grid card carries title, author,
// duration and views in separate siblings, so querying the title only reaches a
// leaf span — walk up to the card to assert the card as a whole carries the data.
function parentWalk(el, fn, depth = 12) {
  let n = el
  for (let i = 0; i < depth && n; i++) {
    if (fn(n)) return n
    n = n.parentElement
  }
  return null
}

const theTitle = (t) => parentWalk(screen.getByText(t), (n) => /card-surface/.test(n.className || ''))

// resolveIn: queue one mock result per id, type the ids into the input box and
// click Resolve, then let the promises settle.
async function resolveIn(values, results) {
  results.forEach((r) => {
    if (r && r.error) adminMock.mockRejectedValueOnce(r.error)
    else adminMock.mockResolvedValueOnce(r.value)
  })
  const box = screen.getByRole('textbox', { name: /iwara url or video id/i })
  fireEvent.change(box, { target: { value: values } })
  fireEvent.click(screen.getByRole('button', { name: /^Resolve$/ }))
  await flush()
  return values
}

beforeEach(() => {
  adminMock.mockReset()
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: vi.fn(async () => {}) },
    configurable: true,
  })
})

describe('format helpers', () => {
  it('renders durations as m:ss and h:mm:ss, and missing as an em dash', () => {
    expect(fmtDuration(0)).toBe('—')
    expect(fmtDuration(undefined)).toBe('—')
    expect(fmtDuration(182)).toBe('3:02')
    expect(fmtDuration(3600)).toBe('1:00:00')
    expect(fmtDuration(3661)).toBe('1:01:01')
  })

  it('compacts view counts so the column stays narrow', () => {
    expect(fmtCount(0)).toBe('—')
    expect(fmtCount(950)).toBe('950')
    expect(fmtCount(12800)).toBe('12.8k')
    expect(fmtCount(1500000)).toBe('1.5M')
  })

  it('renders unix seconds as a local timestamp and zero as an em dash', () => {
    expect(fmtDate(0)).toBe('—')
    expect(fmtDate(null)).toBe('—')
    // 1728000000 = 2024-10-04T05:20:00Z; only the shape is asserted, since the
    // timezone differs per runner and the value itself is not the contract.
    expect(fmtDate(1728000000)).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  })

  it('builds the source page URL from the video id', () => {
    expect(iwaraUrl(video())).toBe('https://www.iwara.tv/videos/ab12cd34')
  })

  it('splits a pasted batch on spaces, commas and full-width commas', () => {
    expect(parseIds('')).toEqual([])
    expect(parseIds(null)).toEqual([])
    expect(parseIds('  ab12cd34  ef56  ')).toEqual(['ab12cd34', 'ef56'])
    expect(parseIds('ab12cd34,ef56')).toEqual(['ab12cd34', 'ef56'])
    expect(parseIds('ab12cd34，ef56\u3001')).toEqual(['ab12cd34', 'ef56'])
  })
})

describe('netdisk layout', () => {
  it('starts in grid view showing the labelled sample entry with no live data', () => {
    renderAt('/iwara')
    expect(screen.getByRole('heading', { name: 'Iwara' })).toBeTruthy()
    expect(screen.getByText('Sample video (placeholder)')).toBeTruthy()
    // The grid card shows the cover placeholder and the sample badge.
    expect(screen.getByText('No preview')).toBeTruthy()
    expect(screen.getByText('Sample data')).toBeTruthy()
    // And it never pretends to have a download link.
    const dl = screen.getByText('Download')
    expect(dl.getAttribute('href')).toBeNull()
    expect(dl.getAttribute('aria-disabled')).toBe('true')
    expect(screen.getByText(/iwara module is not enabled on this node/)).toBeTruthy()
    expect(adminMock).not.toHaveBeenCalled()
  })

  it('renders resolved videos as grid cards carrying the iwara data', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video() }])
    expect(adminMock).toHaveBeenCalledWith('GET', '/iwara/video/ab12cd34')
    const card = theTitle('iwara tool v2.1 release')
    expect(card.textContent).toContain('IwaraTool')
    expect(card.textContent).toContain('3:02')
    expect(card.textContent).toContain('12.8k')
    expect(card.textContent).toContain('views')
    // The cover image is the real one, not the placeholder.
    expect(screen.getByRole('img', { name: /preview of iwara tool/ }).getAttribute('src'))
      .toBe('https://img.iwara.tv/thumb/ab12cd34.jpg')
    // Resolving real data clears the sample notice.
    expect(screen.queryByText(/iwara module is not enabled/)).toBeNull()
  })

  it('switches from grid to a table list with the netdisk columns', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video() }])
    expect(screen.queryByRole('table')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /^List$/ }))
    const head = screen.getByRole('table').querySelector('thead').textContent
    for (const col of ['Preview', 'Title', 'Author', 'Duration', 'Views', 'Uploaded', 'Actions']) {
      expect(head).toContain(col)
    }
  })

  it('shows the uploaded date and an em dash when it is missing', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34 ef56', [
      { value: video({ title: 'first' }) },
      { value: video({ id: 'ef56', title: 'second', author: 'Other', uploaded: 0 }) },
    ])
    fireEvent.click(screen.getByRole('button', { name: /^List$/ }))
    const rows = screen.getByRole('table').querySelectorAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toMatch(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}/)
    expect(rows[1].textContent).toContain('—')
  })

  it('shows a placeholder when the backend returned no cover url', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video({ cover: '' }) }])
    expect(screen.getByText('No preview')).toBeTruthy()
  })

  it('restores the sample entry from the Sample button', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video() }])
    expect(screen.getByText('iwara tool v2.1 release')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /^Sample$/ }))
    expect(screen.getByText('Sample video (placeholder)')).toBeTruthy()
    expect(screen.getByText('Sample data, not a real video')).toBeTruthy()
  })
})

describe('row actions', () => {
  it('downloads the quality the user picked, switching when it changes', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video() }])
    const pick = screen.getByRole('combobox', { name: /quality for iwara tool v2.1 release/i, strict: false })
    expect(pick.value).toBe('Source') // first resolution is the default
    const download = () => screen.getAllByRole('link', { name: /Download/ })[0]
    expect(download().getAttribute('href'))
      .toBe('https://v-f007.v.ihstatic.com/src.mp4?token=t1')
    fireEvent.change(pick, { target: { value: '1080p' } })
    expect(download().getAttribute('href'))
      .toBe('https://v-f007.v.ihstatic.com/hi.mp4?token=t2')
  })

  it('hides the quality select when only one resolution exists', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video({ resolutions: [{ name: 'Source', downloadUrl: 'https://cdn/x.mp4' }] }) }])
    expect(screen.queryByRole('combobox', { name: /quality for/ })).toBeNull()
    expect(screen.getAllByRole('link', { name: /Download/ })[0].getAttribute('href')).toBe('https://cdn/x.mp4')
  })

  it('copies the picked download url to the clipboard and reports it', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video() }])
    fireEvent.click(screen.getByRole('button', { name: /Copy link/ }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalled())
    expect(navigator.clipboard.writeText)
      .toHaveBeenCalledWith('https://v-f007.v.ihstatic.com/src.mp4?token=t1')
    await waitFor(() => expect(screen.getByText('Copied')).toBeTruthy())
  })

  it('links Open to the iwara source page', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: video() }])
    const open = screen.getAllByRole('link', { name: /^Open$/i, strict: false })[0]
    expect(open.getAttribute('href')).toBe('https://www.iwara.tv/videos/ab12cd34')
  })
})

describe('filtering', () => {
  async function withTwoAuthors() {
    renderAt('/iwara')
    await resolveIn('ab12cd34 ef56', [
      { value: video({ title: 'tool release', author: 'IwaraTool' }) },
      { value: video({ id: 'ef56', title: 'other clip', author: 'OtherUploader' }) },
    ])
  }

  it('lists distinct authors in the author filter', async () => {
    await withTwoAuthors()
    const filter = screen.getByRole('combobox', { name: /filter by author/i })
    const options = [...filter.options].map((o) => o.textContent)
    expect(options).toContain('All authors')
    expect(options).toContain('IwaraTool')
    expect(options).toContain('OtherUploader')
  })

  it('narrows the visible entries when an author is selected', async () => {
    await withTwoAuthors()
    expect(screen.getByText(/2 entries/)).toBeTruthy()
    fireEvent.change(screen.getByRole('combobox', { name: /filter by author/i }), { target: { value: 'OtherUploader' } })
    expect(screen.getByText(/1 entry/)).toBeTruthy()
    expect(screen.getByText('other clip')).toBeTruthy()
    expect(screen.queryByText('tool release')).toBeNull()
  })

  it('searches title and author text', async () => {
    await withTwoAuthors()
    const box = screen.getByRole('textbox', { name: /search entries/i })
    fireEvent.change(box, { target: { value: 'OTHERUP' } })
    expect(screen.getByText('other clip')).toBeTruthy()
    expect(screen.queryByText('tool release')).toBeNull()
    fireEvent.change(box, { target: { value: 'release' } })
    expect(screen.getByText('tool release')).toBeTruthy()
  })

  it('shows the empty state when a filter matches nothing', async () => {
    await withTwoAuthors()
    fireEvent.change(screen.getByRole('textbox', { name: /search entries/i }), { target: { value: 'zzzz-no-match' } })
    expect(screen.getByText('No entries')).toBeTruthy()
    expect(screen.getByText(/Clear the filters/)).toBeTruthy()
    expect(screen.queryByRole('table')).toBeNull()
  })
})

describe('data source', () => {
  it('splits a pasted batch of ids', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34 ef56', [
      { value: video() },
      { value: video({ id: 'ef56' }) },
    ])
    expect(adminMock.mock.calls[0]).toEqual(['GET', '/iwara/video/ab12cd34'])
    expect(adminMock.mock.calls[1]).toEqual(['GET', '/iwara/video/ef56'])
  })

  it('encodes a pasted url before putting it in the path', async () => {
    renderAt('/iwara')
    await resolveIn('https://www.iwara.tv/videos/ab12cd34?foo=1', [{ value: video() }])
    expect(adminMock.mock.calls[0]).toEqual(['GET', '/iwara/video/https%3A%2F%2Fwww.iwara.tv%2Fvideos%2Fab12cd34%3Ffoo%3D1'])
  })

  it('deep links: /iwara/:id resolves on mount', async () => {
    adminMock.mockResolvedValueOnce(video({ id: 'ef56' }))
    renderAt('/iwara/ef56')
    await flush()
    expect(adminMock).toHaveBeenCalledTimes(1)
    expect(adminMock.mock.calls[0]).toEqual(['GET', '/iwara/video/ef56'])
  })

  it('keeps the ids that worked when some fail', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34 noper', [
      { value: video() },
      { error: wsErr('HTTP 404', { error: 'video not found' }) },
    ])
    expect(screen.getByText('iwara tool v2.1 release')).toBeTruthy()
    expect(screen.getByText(/Resolved 1 of 2/)).toBeTruthy()
    expect(screen.getByText(/1 failed/)).toBeTruthy()
  })

  it('rejects an empty input without calling ws', () => {
    renderAt('/iwara')
    fireEvent.click(screen.getByRole('button', { name: /^Resolve$/ }))
    expect(adminMock).not.toHaveBeenCalled()
    expect(screen.getByText('Enter an iwara URL or video id')).toBeTruthy()
  })

  it('shows Resolving… and disables the button while a request is in flight', async () => {
    renderAt('/iwara')
    let settle
    adminMock.mockReturnValue(new Promise((r) => { settle = r }))
    const box = screen.getByRole('textbox', { name: /iwara url or video id/i })
    fireEvent.change(box, { target: { value: 'ab12cd34' } })
    fireEvent.click(screen.getByRole('button', { name: /^Resolve$/ }))
    await flush(1)
    const busy = screen.getByRole('button', { name: /^Resolving\.\.\.$/ })
    expect(busy.disabled).toBe(true)
    await act(async () => { settle(video()) })
    await flush()
    expect(screen.getByRole('button', { name: /^Resolve$/ })).toBeTruthy()
    expect(screen.getByText('iwara tool v2.1 release')).toBeTruthy()
  })

  it('falls back to sample data when the module is disabled (gin string 404 body)', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ error: wsErr('HTTP 404', '404 page not found') }])
    expect(screen.getByText(/does not have the iwara module enabled/)).toBeTruthy()
    expect(screen.getByText(/Showing sample data/)).toBeTruthy()
    expect(screen.getByText('Sample video (placeholder)')).toBeTruthy()
  })

  it('shows the backend error instead of pretending success', async () => {
    renderAt('/iwara')
    await resolveIn('noper', [{ error: wsErr('HTTP 404', { error: 'iwara: video not found' }) }])
    expect(screen.getByText('HTTP 404')).toBeTruthy()
    expect(screen.queryByText('iwara tool v2.1 release')).toBeNull()
  })

  it('reports an offline ws session separately from a backend error', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ error: wsErr('ws: not connected') }])
    expect(screen.getByText(/Not connected to a local node/)).toBeTruthy()
  })

  it('ignores null bodies instead of crashing the renderers', async () => {
    renderAt('/iwara')
    await resolveIn('ab12cd34', [{ value: null }])
    expect(screen.queryByText('iwara tool v2.1 release')).toBeNull()
    expect(screen.getByText('Sample video (placeholder)')).toBeTruthy()
  })
})
