// Iwara.jsx — iwara.tv 视频页面，版式仿 BBS 帖子（bbs.imoutolove.me/read.php 的 tpc_content 骨架）。
//
// 数据通道：ws.admin('GET', '/iwara/video/:id') → 后端 controller.GetIwaraVideo →
// echproxy.GetVideoMeta（/video/{id} → fileUrl → X-Version → resolution list）。
// 节点未启用 iwara 模块时（PEERDRIVE_IWARA_ENABLE 默认关：该模块要起外部 ech-proxy
// 进程并注入用户 cookie）该路由不存在，ws.admin 收到的是 gin 的字符串 404 体，
// 页面据此退回示例条目，版式仍可看；模块启用但视频解析失败时返回 JSON {error}，
// 页面显示错误而不是假装成功。
//
// 版式映射（帖子骨架 → iwara 内容）：
//   左栏作者信息(~185px) → iwara 上传者：昵称=作者、UID=authorId、竖排统计=状态/精华/
//                         观看/时长/分辨率/发布时间
//   右栏 tiptop          → 楼层徽标 + 发布时间 + 操作下拉(只看楼主/屏蔽) + 字号 A-/A/A+
//   <h1> subject_tpc     → 视频标题
//   正文 .f14            → 卖点段落 → 封面(border:0, 680px 上限) → 分辨率表 → 下载入口
//   底部 readbot         → 顶端(滚动) / 回复(剪贴板) / 引用(剪贴板)

import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import * as ws from '../ws'

// 字号切换（对应帖子 tiptop 右侧的 A-/A/A+）
const FONT_SIZES = [
  { key: 'small', label: 'A-', px: 12 },
  { key: 'medium', label: 'A', px: 14 }, // .f14 原版正文
  { key: 'large', label: 'A+', px: 16 },
]

// 示例条目：后端不可用时保持版式可看。demo:true → 下载按钮禁用（不给假链接）、
// 封面用纯色块而不是外链图（测试环境不联网）。
const DEMO_ENTRY = {
  id: 'demo-0001',
  title: '【IwaraTool】I站下载器 · 版式示例条目',
  status: 'published',
  rating: 'general',
  author: 'IwaraTool',
  authorId: 'u-demo',
  cover: '',
  duration: 182,
  views: 12800,
  uploaded: 0,
  file: { name: 'demo.mp4' },
  resolutions: [
    { id: 'src', name: 'Source', downloadUrl: '' },
    { id: 'hi', name: '1080p', downloadUrl: '' },
    { id: 'sd', name: '480p', downloadUrl: '' },
  ],
  demo: true,
}

// ── 纯展示助手 ─────────────────────────────────────────────────────────────

// fmtDuration: 秒 → m:ss / h:mm:ss。0 与缺省都渲染 '—'，不假装时长已知。
export function fmtDuration(sec) {
  if (!sec || sec <= 0) return '—'
  const s = Math.round(sec)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const r = s % 60
  const p2 = (n) => String(n).padStart(2, '0')
  return h ? `${h}:${p2(m)}:${p2(r)}` : `${m}:${p2(r)}`
}

// fmtCount: 观看数 → 1.3万 / 12.8k / 1.2M（原版帖子习惯用 k/M 简写）
export function fmtCount(n) {
  if (!n || n <= 0) return '—'
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 1e4) return `${(n / 1e3).toFixed(1)}k`
  return String(n)
}

// fmtDate: Unix 秒 → 'YYYY-MM-DD HH:mm'。0 渲染 '—'。
export function fmtDate(ts) {
  if (!ts || ts <= 0) return '—'
  const d = new Date(ts * 1000)
  if (Number.isNaN(d.getTime())) return '—'
  const p2 = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`
}

export function iwaraUrl(entry) {
  return `https://www.iwara.tv/videos/${entry.id}`
}

// copyText: navigator.clipboard 在非安全上下文/happy-dom 里不存在，退回
// execCommand；两条路都失败才返回 false（按钮仍能显示"复制失败"而不是静默）。
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      document.body.removeChild(ta)
      return !!ok
    } catch {
      return false
    }
  }
}

// Avatar: 作者首字母 + 由名字派生的稳定色相（不用随机色，避免每次渲染跳色）
function Avatar({ name }) {
  const letter = ((name || '?').trim().charAt(0) || '?').toUpperCase()
  let h = 0
  for (const ch of (name || '')) h = (h * 31 + (ch.codePointAt(0) || 0)) % 360
  return (
    <div aria-label={`avatar of ${name || 'unknown'}`}
      className="flex items-center justify-center rounded-full font-bold text-white"
      style={{ width: 56, height: 56, fontSize: 22, background: `hsl(${h} 52% 38%)` }}>
      {letter}
    </div>
  )
}

// RatingBadge: BBS 左栏的「精华」位置，这里放 iwara 的 rating 标签
function RatingBadge({ rating }) {
  if (!rating) return <span className="gray">—</span>
  const explicit = /explicit|adult/i.test(rating)
  return (
    <span className="badge-soft" style={{
      borderColor: explicit ? 'rgba(244,63,94,0.5)' : 'rgba(99,102,241,0.4)',
      color: explicit ? '#fda4af' : '#a5b4fc',
      background: explicit ? 'rgba(244,63,94,0.12)' : 'rgba(99,102,241,0.12)',
    }}>{rating}</span>
  )
}

// IwaraPost: 一条 iwara 视频 = 一个 BBS 帖子。floor 是它在列表中的楼层号。
function IwaraPost({ entry, floor, fontPx, fontKey, onFontSize, isOp, onFilterAuthor,
                      onBlockAuthor, showOnlyOp, blockedAuthor, onCopy, onTop }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [copiedKey, setCopiedKey] = useState('')
  const resolutions = Array.isArray(entry.resolutions) ? entry.resolutions : []
  const author = entry.author || '未知作者'
  const disabled = !!entry.demo

  // 卖点段落：把元数据压成一段可扫读的说明（对应帖子里"卖点段落 → 截图 → 下载"的节奏）
  const pitch = [
    `${author} 的作品《${entry.title}》`,
    `时长 ${fmtDuration(entry.duration)}`,
    `观看 ${fmtCount(entry.views)}`,
    resolutions.length ? `共 ${resolutions.length} 个清晰度可选` : null,
    entry.file?.name ? `源文件 ${entry.file.name}` : null,
  ].filter(Boolean).join(' · ')

  const doCopy = async (key, text) => {
    const ok = await copyText(text)
    onCopy(entry.id, ok)
    setCopiedKey(ok ? key : '')
    if (ok) setTimeout(() => setCopiedKey(''), 1800)
  }

  const mark = (text) => (copiedKey === text ? ' ✓ 已复制' : '')

  return (
    <article className="tpc_content" data-post={entry.id} aria-label={entry.title}>
      <div className="r_one">
        {/* ── 左栏作者信息区（~185px）── */}
        <div className="authorbox f12">
          <Avatar name={author} />
          <div className="mt-2 text-center font-bold" style={{ color: '#e5e7eb', fontSize: 14 }}>
            {author}
          </div>
          <dl className="mt-3 space-y-1">
            <div className="flex justify-between gap-2"><dt className="gray">UID</dt><dd className="truncate">{entry.authorId || '—'}</dd></div>
            <div className="flex justify-between gap-2"><dt className="gray">状态</dt><dd>{entry.status || '—'}</dd></div>
            <div className="flex justify-between gap-2"><dt className="gray">精华</dt><dd><RatingBadge rating={entry.rating} /></dd></div>
            <div className="flex justify-between gap-2"><dt className="gray">观看</dt><dd>{fmtCount(entry.views)}</dd></div>
            <div className="flex justify-between gap-2"><dt className="gray">时长</dt><dd>{fmtDuration(entry.duration)}</dd></div>
            <div className="flex justify-between gap-2"><dt className="gray">清晰度</dt><dd>{resolutions.length || '—'}</dd></div>
            <div className="flex justify-between gap-2"><dt className="gray">发布时间</dt><dd>{fmtDate(entry.uploaded)}</dd></div>
          </dl>
        </div>

        {/* ── 右栏帖子主体 ── */}
        <div className="post">
          <div className="tiptop f12">
            <span className="floor">楼层 {floor}</span>
            {isOp && <span className="gray">楼主</span>}
            <span className="gray">{fmtDate(entry.uploaded)}</span>
            <span className="ml-auto flex items-center gap-1">
              {/* 操作下拉：只看楼主 / 屏蔽（原版 tiptop 的操作菜单） */}
              <div className="relative">
                <button type="button" aria-haspopup="menu" aria-expanded={menuOpen}
                  onClick={() => setMenuOpen((v) => !v)}
                  className="rounded px-2 py-1 border border-white/10 bg-white/[0.04] hover:bg-white/[0.08]">
                  操作 ▾
                </button>
                {menuOpen && (
                  <div role="menu" className="absolute right-0 z-10 mt-1 w-32 rounded-lg border border-white/10 bg-[#17171c] shadow-lg py-1">
                    <button type="button" role="menuitem"
                      className={`block w-full text-left px-3 py-1.5 hover:bg-white/[0.06] ${showOnlyOp ? 'text-brand-300' : ''}`}
                      onClick={() => { onFilterAuthor(author); setMenuOpen(false) }}>
                      只看楼主
                    </button>
                    <button type="button" role="menuitem"
                      className={`block w-full text-left px-3 py-1.5 hover:bg-white/[0.06] ${blockedAuthor ? 'text-rose-300' : ''}`}
                      onClick={() => { onBlockAuthor(author); setMenuOpen(false) }}>
                      屏蔽{blockedAuthor ? '已开启' : ''}
                    </button>
                  </div>
                )}
              </div>
              {/* 字号切换 */}
              <div className="flex items-center gap-0.5 border-l border-white/10 pl-2">
                {FONT_SIZES.map((fs) => (
                  <button key={fs.key} type="button" title={`${fs.label} 字号`}
                    onClick={() => onFontSize?.(fs.key)}
                    className={`px-1.5 py-1 rounded ${fs.key === fontKey ? 'bg-white/10 text-white' : 'text-gray-400 hover:text-white'}`}
                    style={{ fontSize: fs.px }}>
                    {fs.label}
                  </button>
                ))}
              </div>
            </span>
          </div>

          {/* 标题区 */}
          <h1 className="subject_tpc">{entry.title}</h1>

          {/* 正文区：14px 正文，图片 border:0 + 680px 上限 */}
          <div className="posttext f14" style={{ fontSize: fontPx }}>
            <p className="mb-3">{pitch}</p>

            {/* 媒体区：封面（demo 模式用色块占位，不加载外链） */}
            {entry.cover ? (
              <p className="my-3"><img src={entry.cover} alt={`cover of ${entry.title}`} /></p>
            ) : (
              <p className="my-3">
                <div className="flex items-center justify-center rounded-lg text-gray-400 f12"
                  style={{ width: '100%', maxWidth: 680, height: 96, background: 'rgba(99,102,241,0.10)', border: '1px dashed rgba(255,255,255,0.14)' }}>
                  {disabled ? '示例条目 · 无封面' : '该视频没有返回封面图'}
                </div>
              </p>
            )}

            {/* 分辨率选项 */}
            {resolutions.length > 0 && (
              <table className="w-full my-3 border-collapse" style={{ maxWidth: 680 }}>
                <thead>
                  <tr className="text-left f12">
                    <th className="gray px-2 py-1.5 border-b border-white/10 font-normal">清晰度</th>
                    <th className="gray px-2 py-1.5 border-b border-white/10 font-normal">下载</th>
                    <th className="gray px-2 py-1.5 border-b border-white/10 font-normal">链接</th>
                  </tr>
                </thead>
                <tbody>
                  {resolutions.map((r) => (
                    <tr key={r.id || r.name} className="border-b border-white/[0.05]">
                      <td className="px-2 py-1.5 whitespace-nowrap">{r.name || '—'}</td>
                      <td className="px-2 py-1.5">
                        <a href={disabled ? undefined : r.downloadUrl} download
                          className="btn-brand" style={{ padding: '4px 12px', fontSize: 12, opacity: disabled ? 0.45 : 1 }}
                          {...(disabled ? { 'aria-disabled': true, onClick: (e) => e.preventDefault() } : { target: '_blank', rel: 'noreferrer' })}>
                          ⬇ 下载
                        </a>
                      </td>
                      <td className="px-2 py-1.5">
                        <button type="button" onClick={() => doCopy('res:' + (r.id || r.name), r.downloadUrl)}
                          className="rounded px-2 py-1 text-gray-400 hover:text-white border border-white/10 hover:bg-white/[0.06]"
                          disabled={disabled || !r.downloadUrl}>
                          复制{mark('res:' + (r.id || r.name))}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            {/* 下载/入口链接 */}
            <p className="mt-3 f12">
              <a href={iwaraUrl(entry)} target="_blank" rel="noreferrer">打开 Iwara 原页面</a>
              {entry.file?.name && <span className="gray"> · 源文件 {entry.file.name}</span>}
            </p>
          </div>
        </div>
      </div>

      {/* 底部操作条：顶端 / 回复 / 引用 */}
      <div className="readbot">
        <button type="button" onClick={onTop} className="rounded px-2.5 py-1 text-gray-300 border border-white/10 hover:bg-white/[0.06]">
          ↑ 顶端
        </button>
        <button type="button" onClick={() => doCopy('reply',
          `回复 ${author} 的《${entry.title}》：${iwaraUrl(entry)}`)}
          className="rounded px-2.5 py-1 text-gray-300 border border-white/10 hover:bg-white/[0.06]">
          ↩ 回复{mark('reply')}
        </button>
        <button type="button" onClick={() => doCopy('quote',
          `> ${entry.title}\n> ${author} · ${fmtDuration(entry.duration)} · ${fmtCount(entry.views)} 次观看\n> ${iwaraUrl(entry)}`)}
          className="rounded px-2.5 py-1 text-gray-300 border border-white/10 hover:bg-white/[0.06]">
          ❞ 引用{mark('quote')}
        </button>
        <span className="ml-auto gray f12">{resolutions.filter((r) => r.downloadUrl).length} 个下载入口</span>
      </div>
    </article>
  )
}

// Iwara: 页面外壳——输入框 + 条目列表 + 数据源提示
export default function Iwara() {
  const { id } = useParams()
  const [input, setInput] = useState('')
  const [entries, setEntries] = useState([DEMO_ENTRY])
  const [demoMode, setDemoMode] = useState(true)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [fontKey, setFontKey] = useState('medium')
  const [showOnlyOp, setShowOnlyOp] = useState('')   // 只看楼主：锚定作者
  const [blockedAuthor, setBlockedAuthor] = useState('')

  const fontPx = FONT_SIZES.find((f) => f.key === fontKey)?.px || 14

  // URL 里的 :id 直接解析（/iwara/:id 深链接）
  useEffect(() => {
    if (id) void parse([id])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  // parse: 把一个或多个 iwara ID/URL 解析成条目。
  // 失败判定分三类（ws.admin 的 reject 形态不同）：
  //   err.data 是字符串  → gin 的 "404 page not found"，路由不存在 = 模块没启用 → 退回示例条目
  //   err.data 是对象     → 模块启用了但视频解析失败 → 显示错误，不假装成功
  //   err.data 为空       → WS 没连上 → 提示连节点
  const parse = useCallback(async (ids) => {
    const clean = ids.map((s) => String(s).trim()).filter(Boolean)
    if (clean.length === 0) { setError('请输入 iwara 链接或视频 ID'); return }
    setLoading(true)
    setError('')
    const ok = []
    const fails = []
    for (const raw of clean) {
      try {
        const meta = await ws.admin('GET', '/iwara/video/' + encodeURIComponent(raw))
        // 空响应不塞进列表：渲染层直接读 entry.author 会炸，比少显示一条更糟
        if (meta) ok.push(meta)
      } catch (e) {
        fails.push({ raw, err: e })
      }
    }
    setLoading(false)
    if (ok.length > 0) {
      setEntries(ok)
      setDemoMode(false)
      setNotice(`已解析 ${ok.length} 条${fails.length ? `，${fails.length} 条失败` : ''}`)
    } else if (fails.some((f) => typeof f.err.data === 'string')) {
      // 路由不存在：节点未启用 iwara 模块
      setEntries([DEMO_ENTRY])
      setDemoMode(true)
      setError('节点未启用 iwara 模块（后端 PEERDRIVE_IWARA_ENABLE=false，默认关）。当前显示示例条目，用于查看版式。')
    } else if (fails[0]?.err?.data && typeof fails[0].err.data === 'object') {
      setEntries([DEMO_ENTRY])
      setDemoMode(true)
      setError(fails[0].err.message || '解析失败')
    } else {
      setEntries([DEMO_ENTRY])
      setDemoMode(true)
      setError('未连接到本地节点，无法解析 iwara 条目。')
    }
  }, [])

  const submit = (e) => {
    e.preventDefault()
    setNotice('')
    void parse(input.split(/[\s,，、]+/))
  }

  const loadDemo = () => {
    setEntries([DEMO_ENTRY])
    setDemoMode(true)
    setError('')
    setNotice('示例条目（演示数据，非真实视频）')
  }

  // 只看楼主 / 屏蔽 都是对「当前可见条目」的过滤，锚定作者来自点击下拉的那个帖子
  const visible = useMemo(() => {
    let list = entries
    if (showOnlyOp) list = list.filter((e) => (e.author || '未知作者') === showOnlyOp)
    if (blockedAuthor) list = list.filter((e) => (e.author || '未知作者') !== blockedAuthor)
    return list
  }, [entries, showOnlyOp, blockedAuthor])

  const scrollTop = () => {
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  const onCopy = useCallback(() => { /* 复制状态由各帖子自己显示 */ }, [])

  return (
    <div className="p-6 h-full overflow-y-auto iwara-bbs">
      <div className="max-w-[900px] mx-auto space-y-4">
        {/* 输入区 */}
        <div className="card-surface p-4">
          <h2 className="text-lg font-semibold text-gray-100 mb-1">Iwara 视频</h2>
          <p className="f12 gray mb-3">
            粘贴 iwara 链接或视频 ID（空格/逗号分隔可批量）。数据由后端
            <code className="text-gray-400"> /iwara/video/:id </code>
            经 ech-proxy 取回：标题 / 作者 / 时长 / 封面 / 各清晰度下载地址。
          </p>
          <form onSubmit={submit} className="flex gap-2">
            <input className="input-base flex-1" value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="https://www.iwara.tv/videos/xxxxxxxx 或 视频ID"
              aria-label="iwara URL or video id" />
            <button type="submit" disabled={loading || !input.trim()} className="btn-brand">
              {loading ? '解析中…' : '解析'}
            </button>
            <button type="button" onClick={loadDemo} className="btn-ghost">示例条目</button>
          </form>
          {error && (
            <p className="mt-3 text-rose-300 f12" role="alert">{error}</p>
          )}
          {notice && !error && (
            <p className="mt-3 text-gray-400 f12">{notice}</p>
          )}
        </div>

        {/* 过滤状态 */}
        {(showOnlyOp || blockedAuthor) && (
          <div className="flex items-center gap-2 f12">
            {showOnlyOp && (
              <span className="badge-soft text-brand-300 border-brand-400/40 bg-brand-500/10">
                只看 {showOnlyOp}
                <button type="button" onClick={() => setShowOnlyOp('')} className="ml-1 hover:text-white" aria-label="取消只看楼主">×</button>
              </span>
            )}
            {blockedAuthor && (
              <span className="badge-soft text-rose-300 border-rose-400/40 bg-rose-500/10">
                已屏蔽 {blockedAuthor}
                <button type="button" onClick={() => setBlockedAuthor('')} className="ml-1 hover:text-white" aria-label="取消屏蔽">×</button>
              </span>
            )}
          </div>
        )}

        {/* 帖子列表：每条 iwara 视频 = 一个帖子 */}
        <div className="space-y-4">
          {visible.map((entry, i) => (
            <IwaraPost key={entry.id + i} entry={entry} floor={i + 1}
              fontPx={fontPx} fontKey={fontKey} isOp
              onFontSize={setFontKey}
              onFilterAuthor={(a) => setShowOnlyOp((cur) => (cur === a ? '' : a))}
              onBlockAuthor={(a) => setBlockedAuthor((cur) => (cur === a ? '' : a))}
              showOnlyOp={showOnlyOp === (entry.author || '未知作者')}
              blockedAuthor={blockedAuthor === (entry.author || '未知作者')}
              onTop={scrollTop} onCopy={onCopy} />
          ))}
          {visible.length === 0 && (
            <div className="card-surface p-8 text-center text-gray-400 f14">
              {entries.length === 0 ? '还没有条目' : '当前过滤条件下没有可见条目'}
            </div>
          )}
        </div>

        <p className="f12 gray text-center pb-4">
          {demoMode ? '示例条目为演示数据' : '条目数据来自 iwara.tv API（经本节点 ech-proxy 转发）'}
        </p>
      </div>
    </div>
  )
}
