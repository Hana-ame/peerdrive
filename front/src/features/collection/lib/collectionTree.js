// collectionTree.js — pure tree-building logic for the collection folder view.
//
// 为什么拆成纯函数模块：目录树的构建/归一化不依赖 React 与 WS，可以在
// vitest 里直接单测（格式兼容矩阵 + 冲突路径是典型的"肉眼看着对、边界错"
// 逻辑），组件只做渲染。CollectionView 页面、CollectionBrowser 组件、测试
// 三方共用这一份判定，避免各写一份解析。
//
// 目标 collection JSON 结构（本分支前端期望、写给后端对齐）：
//   { "version": 3, "friendly_name": "...", "created_at": "...",
//     "entries": [
//       { "path": "photos/trip/a.jpg", "sha": "<64hex>", "preview": "<64hex>", "size": 12345, "mime_type": "image/jpeg" },
//       { "path": "docs/readme.md", "sha": "<64hex>", "preview": "" }
//     ] }
// - path：POSIX 风格相对路径，分隔符 "/"（兼容 "\" 与首尾多余斜杠）
// - sha：文件内容 sha256（64hex），叶子条目的取数/下载键
// - preview：预览文件 sha256（通常是图片），可为空字符串/缺省表示无预览
// - size / mime_type：可选展示元数据
//
// 兼容读入（当前后端/历史格式，见 back/internal/model/anon.go）：
//   1. 旧格式 entries[].hash
//   2. 现格式 entries[].providers[]（sha256 provider 的 value）
// 这样在"新格式后端落地前"，同一视图就能直接消费 /anon/collections/{hash}
// 返回的现存 JSON，不会因为格式切换而双轨维护。

// normalizeEntry 把单条条目归一成叶子文件节点数据 {path, sha, preview, size, mime}。
// 返回 null 表示条目非法（缺 path 或无法解析出任何取数键），buildTree 直接跳过。
// 为什么 sha 缺失仍保留为"可展示但不可操作"：文件夹视图的首要职责是把 collection
// 的层级结构呈现出来；一条缺 sha 的条目（脏数据/目录占位）仍应出现在树里让人看到，
// 只是取数/下载按钮置灰。
export function normalizeEntry(entry) {
  if (!entry || typeof entry !== 'object') return null
  const path = typeof entry.path === 'string' ? entry.path.trim() : ''
  if (!path) return null

  // 取数键优先级：新格式 sha > 旧格式 hash > providers[].sha256.value。
  // 为什么先取 entry.sha 再取 entry.hash：新格式落地后 sha 是主字段；hash 只是
  // 后端 MarshalJSON 为了旧客户端兼容顺带输出的（anon.go MarshalJSON），两者
  // 内容等价，先主后次即可。
  let sha = typeof entry.sha === 'string' ? entry.sha : ''
  if (!sha && typeof entry.hash === 'string') sha = entry.hash
  let mime = typeof entry.mime_type === 'string' ? entry.mime_type : ''
  if (Array.isArray(entry.providers)) {
    for (const p of entry.providers) {
      if (!p) continue
      if (!sha && p.type === 'sha256' && typeof p.value === 'string' && p.value) sha = p.value
      if (!mime && typeof p.mime_type === 'string' && p.mime_type) mime = p.mime_type
    }
  }

  // preview 只认约定字段名（可空字符串/缺省），不猜别名：契约越宽越难对齐，
  // 后端 PR 按本文档字段名实现即可。
  const preview = typeof entry.preview === 'string' ? entry.preview : ''
  const size = typeof entry.size === 'number' && Number.isFinite(entry.size) ? entry.size : null
  return { path, sha, preview, size, mime }
}

// splitPath 把条目 path 切成段数组（叶子段是文件名，其余是目录名）。
// 为什么在这里统一分隔符：Windows 风格路径用 "\"；collection 条目是内容寻址
// JSON，可能来自任意平台节点，统一成 "/" 解析避免一棵树里出现两种分隔的目录。
export function splitPath(path) {
  const norm = String(path)
    .replace(/\\/g, '/')       // Windows 分隔符归一
    .replace(/\/+/g, '/')      // 连续斜杠折叠（防 "a//b" 拆出空段）
    .replace(/^\/+|\/+$/g, '') // 首尾斜杠去掉（"绝对路径感"的条目按相对处理）
  if (!norm) return []
  return norm.split('/')
}

// basename 取路径最后一段作为展示名（叶子无最后一段时回退到完整 path）。
export function basename(path) {
  const segs = splitPath(path)
  return segs.length ? segs[segs.length - 1] : String(path || '')
}

// buildTree 把归一化后的条目列表建成目录树：
//   根节点  {type:'dir', name:'', children:[...]}
//   目录节点 {type:'dir', name, children}
//   文件节点 {type:'file', name, entry}   （entry 是 normalizeEntry 的结果）
// children 已排序：目录在前、文件在后，各自按名（大小写不敏感）字典序。
//
// 两条硬性规则（路径冲突时的取舍，务必与测试一致）：
//   1. 同一条 path 出现多次 → 保留第一个（内容寻址集合里重复条目是脏数据）
//   2. 同一名字既是文件又是目录（"a/b" 文件 + "a/b/c" 文件）→ 目录优先：
//      文件让位，仍能从目录进入更深条目。文件夹视图以"能到达最深文件"为第一目标。
export function buildTree(entries) {
  const root = { type: 'dir', name: '', children: [] }
  // trie：dirNode → Map<段名, dirNode | fileNode>。为什么用 Map 而不是直接往
  // children 数组里塞：插入期需要 O(1) 按名查重（数组 find 在千级条目下退化成
  // O(n²)），排序留到 finalize 阶段统一做一次。
  const trie = new Map([[root, new Map()]])
  const seen = new Set()

  for (const raw of entries || []) {
    const entry = normalizeEntry(raw)
    if (!entry) continue
    const segs = splitPath(entry.path)
    if (!segs.length) continue
    const full = segs.join('/')
    if (seen.has(full)) continue // 规则 1：重复 path 只取第一个
    seen.add(full)

    let dir = root
    // 中间段：建/沿目录走
    for (let i = 0; i < segs.length - 1; i++) {
      const seg = segs[i]
      const map = trie.get(dir)
      let next = map.get(seg)
      if (!next) {
        next = { type: 'dir', name: seg, children: [] }
        map.set(seg, next)
        trie.set(next, new Map())
      } else if (next.type !== 'dir') {
        // 规则 2：同名文件让位给目录——文件节点原地转成目录（放弃该文件条目，
        // 保住更深条目）。为什么必须转换而不是丢弃这条深层条目：文件夹视图的
        // 第一目标是"能到达最深文件"，且"目录优先"的语义应与条目先后顺序无关
        // （否则同一份数据换个顺序就长出不同的树）。
        const converted = { type: 'dir', name: seg, children: [] }
        map.set(seg, converted)
        trie.set(converted, new Map())
        next = converted
      }
      dir = next
    }

    const leafName = segs[segs.length - 1]
    const map = trie.get(dir)
    const existing = map.get(leafName)
    if (existing && existing.type === 'dir') continue // 规则 2b：叶子撞目录名 → 目录优先
    map.set(leafName, { type: 'file', name: leafName, entry })
  }

  // finalize：把 trie 递归转成排序 children（插入期不排序，这里只排一次）
  const finalize = (node) => {
    const children = []
    for (const child of trie.get(node).values()) {
      if (child.type === 'dir') finalize(child)
      children.push(child)
    }
    children.sort(cmpNode)
    node.children = children
  }
  finalize(root)
  return root
}

// cmpNode：目录恒在文件前；同类按名排序。sensitivity:'base' 让 "a" 与 "A" 相邻，
// 大小写仅作次排序，符合常见文件管理器的排序直觉。
function cmpNode(a, b) {
  if (a.type !== b.type) return a.type === 'dir' ? -1 : 1
  return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
}

// descend 沿 segments 从树根走到目标目录节点；中途不存在则返回 null。
// 为什么单独抽出来：面包屑/进入目录都靠它定位当前节点，且 currentPath 里
// 可能残留已被刷新掉的段（换了一个 collection 时），返回 null 由调用方重置。
export function descend(root, segments) {
  let node = root
  for (const seg of segments || []) {
    const child = node.children.find((c) => c.type === 'dir' && c.name === seg)
    if (!child) return null
    node = child
  }
  return node
}

// entrySha：对外提供"取一个条目的取数键"，兼容 sha/hash/providers 三种形态。
// Collections 详情页的下载按钮也用这个，避免新旧格式切换后按钮失效。
export function entrySha(entry) {
  const norm = normalizeEntry(entry)
  return norm ? norm.sha : ''
}

// previewSha：取条目的预览取数键（可为空字符串）。
export function previewSha(entry) {
  const norm = normalizeEntry(entry)
  return norm ? norm.preview : ''
}
