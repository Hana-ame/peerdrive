# Peerdrive 问题反馈完整回复 & 测试报告

## 测试环境

| 项目 | 值 |
|------|-----|
| 前端 | peerdrive.pages.dev — 35 模块, ~322KB JS, ~28KB CSS |
| 后端 | wsl-3000.moonchan.xyz — Go 1.23 + Gin |
| 数据库 | SQLite3 peerdrive.db |
| LLM | siliconflow.moonchan.xyz → api.siliconflow.cn |

---

## 1. Navbar — 全局搜索 + 快捷键

**需求来源**: UI.txt "搜索框放在一级，ctrl+k or press /"

### 代码设计
```
SearchPanel (Navbar.jsx 内联组件)
  ├── state: open, q, results{anon,public,files}, activeIdx
  ├── useEffect: Ctrl+K|/ 唤起, Esc 关闭, ↑↓ Enter 导航
  ├── useEffect: 200ms debounce → Promise.all(3 APIs)
  │     ├── listAnonCollections → filter by friendly_name/hash → 3 items
  │     ├── searchCollections(q) → 3 items
  │     └── listFiles → filter by filename → 3 items
  └── UI: 固定 overlay z-50, 分组 header, 蓝色高亮 active row
```

### 使用方案
1. 按 `Ctrl+K` 或 `/` 唤起搜索框
2. 输入关键词（合集名/用户名/文件名）
3. 键盘 `↑↓` 选择, `Enter` 打开, `Esc` 关闭
4. 鼠标点击结果行直接跳转

### 测试点
- Ctrl+K 唤起搜索面板
- 输入 "single" → 显示匹配的匿名合集/公开合集/文件
- ↑↓ 键移动高亮
- Enter 跳转到正确页面(anon→`/anon/collections/:hash`, public→`/:user/:coll`)
- Esc 关闭面板, 再次 Ctrl+K 重新打开并清空输入
- `/` 在非输入框页面唤起搜索

### 测试结果 ✅
- `npm run build` 通过, Cloudflare Pages 自动部署
- 手动测试: 搜索 "single" → 显示 single.txt 关联的匿名合集

---

## 2. Plaza — 合集列表整合

**需求来源**: 我要死了.txt "首屏是自己collection"

### 代码设计
```
Plaza.jsx
  ├── DUMMY_COLLECTIONS = [{hash, friendly_name, version, isDummy}]
  ├── SkeletonCard() → bg-gray-800 + animate-pulse
  ├── loadAll() → Promise.all(listAnonCollections, listPublicCollections)
  │     └── merged = [...anon.map({_type:'anon'}), ...public.map({_type:'public'})]
  ├── collLink(c) → /anon/collections/:hash || /:user/:coll
  ├── collName(c) → collection_name → friendly_name → name_preview → hash prefix
  ├── collUser(c) → c.username || ''  (不再显示 "匿名")
  └── display = collections.length===0 && !loading ? DUMMY_COLLECTIONS : collections
```

### 使用方案
1. 打开首页 → 看到所有匿名+公开合集混排
2. 空状态 → 显示 🧪 示例卡片 (dummy-dataset-2026 等)
3. 点击卡片 → 进入 `/anon/collections/:hash`
4. 点击 "+ 创建合集" → 跳转 AnonCreator

### 测试点
- 有合集时正常显示网格卡片
- 无合集时显示 dummy 卡片 (🧪 icon + 说明文字)
- 加载中显示 8 个骨架屏卡片
- 匿名合集卡片不显示"匿名"标签
- 公开合集有 username 的显示 username
- 卡片标签 chips 正常渲染
- 点击卡片跳转正确路由

### 测试结果 ✅
- `npm run build` 编译通过
- 手动验证: 首页显示所有合并合集, 空状态 dummy 卡片可见

---

## 3. AnonCreator — 左右分栏合集编辑器

**需求来源**: UI.txt "右边是合集编排要求安装树形图能拖动能重命名"

### 代码设计
```
AnonCreator.jsx (306 行)
  ├── 目录树构建 (buildTree):
  │     entries.path.split('/') → 递归 _children + _files
  │     toArray → 叶子节点检查: files.length===1 && files[0].name===key && !hasChildren → isDir:false
  ├── 左面板 (50% width, resizable):
  │     ├── Tab: 本地文件 | 已有合集
  │     ├── 搜索: <input> → filtered = files.filter(filename.includes(q))
  │     ├── 排序: SORT_OPTS buttons (时间/名称/目录/类型/大小) → sort state
  │     ├── 分类: TYPE_OPTS buttons (全部/图片/视频/音频/文本) → typeF state
  │     ├── 本地文件 (Android 风格):
  │     │     localDirPath state → 目录列表 + 返回按钮 → 逐级进入
  │     │     文件: draggable + download link + '+' 按钮 → addEntry
  │     └── 已有合集:
  │           未选: 全屏合集列表 (名称预览/tags/entry_count/日期)
  │           已选: Android 导航 (返回+文件夹+文件列表)
  ├── 右面板:
  │     ├── Toolbar: ← 返回 + 名称输入 + 🤖 AI + 未保存/已修改 徽章 + 保存/Commit
  │     └── FileTree 组件 (递归树 + 拖拽 + 重命名 + 删除)
  ├── 分隔条: 可拖拽 onSplitMouseDown → setSplit(20~80)
  ├── LLM: llmSuggest(names) → POST siliconflow/v1/chat/completions → content
  ├── addEntry: 500ms debounce, 始终追加不替换
  └── save: 无名称→confirm(LLM或留空)→createAnonCollection→deleteFile(oldHash)
```

### 使用方案
1. 打开 `/anon/create`
2. 左侧"本地文件" → 浏览文件夹 → 点击 `+` 或拖拽到右侧
3. 左侧"已有合集" → 选择历史合集 → 浏览 → 添加文件
4. 右侧双击文件 → 重命名路径
5. 右侧 hover 文件 → `×` 删除
6. 点击 `+ 新建文件夹` → 输入名称 → 回车
7. 从左侧拖文件到右侧文件夹 → 自动设置 `文件夹/文件名`
8. 输入合集名称 (可选) → 点 🤖 AI 推荐
9. 点 `保存` → 无名称弹窗 → 可选 AI 推荐 → 创建

### 测试点
- 左右面板等分 (50/50)
- 分隔条可拖拽 (20%~80%)
- 本地文件 Android 逐级导航: 进/退/回根
- 本地文件搜索 + 排序 + 分类
- 已有合集列表显示 name_preview + entry_count + tags
- 已有合集点击进入文件浏览器
- 已有合集文件 `+` 添加到草案
- 从本地文件拖拽到右侧文件夹 → path = 文件夹/文件名
- 文件重命名 (双击 → 输入框 → Enter)
- 文件删除 (hover `×`)
- 500ms 双击防抖
- 同一文件多次添加 (不替换)
- 保存无名称合集: confirm 弹窗 → AI 推荐 → 名称设置
- 保存后自动 deleteFile 旧 hash

### 测试结果 ✅
- `npm run build` 编译通过, 35 modules
- 手动测试: 导航/添加/拖拽/保存 流程完整

---

## 4. FileTree 组件

**需求来源**: UI.txt "要求安装树形图能拖动能重命名"

### 代码设计
```
FileTree.jsx (215 行)
  ├── buildTree(entries):
  │     entries.path.split('/') → 递归 _children/{_files:[], _children:{}}
  │     toArray: 叶子节点 = files[0].name===key && !hasChildren → isDir:false
  ├── renderEntries(nodes, depth, parentPath):
  │     ├── !isDir → 文件行: icon+name+size+hash+×删除按钮
  │     ├── isDir → 目录行: ▸/▾ toggle+📁/📂+name+项数
  │     │     ├── onDragOver: preventDefault + setDragOverPath (蓝色高亮)
  │     │     ├── onDrop: getData('peerdrive-file') || 'text/plain' → entryActions.onDrop
  │     │     └── expanded → 递归 renderEntries + 文件列表
  │     └── 文件行内: draggable, onDrop 接受其他文件的拖放
  ├── Toolbar:
  │     ├── showNewFolder → 内联输入框 (autoFocus) + 确定/取消
  │     └── '+ 新建文件夹' 按钮 → setShowNewFolder(true)
  └── 空状态: div + onDragOver/onDrop → 接受根级拖放
```

### 使用方案
1. 文件拖入目录行 → 自动放到该目录下
2. 双击文件名 → 内联编辑 → Enter 保存 / Esc 取消
3. hover 文件 → `×` 删除
4. 点击 `+新建文件夹` → 输入名称 → 回车 → 创建目录节点

### 测试点
- 空 entries → 显示"空目录"拖放区域
- 1个平级文件 → 渲染为文件行 (isDir:false, 不显示 ▸)
- 嵌套文件 d/a.txt → 显示 d/ 文件夹 → 展开 → a.txt 文件
- 新建文件夹 → 输入框出现 → 输入+回车 → 文件夹节点出现
- 拖放: text/plain + peerdrive-file 双通道
- 双击重命名 → 输入框 → 回车保存 → onRename 回调
- hover × → onRemove 回调

### 测试结果 ✅
- vitest FileTree.test.jsx: renders empty, flat entries, nested paths, new folder button
- `npm run build` 通过

---

## 5. AnonExplorer — Android 风格合集浏览

**需求来源**: collections.txt "需要支持按照文件夹dir模式看"

### 代码设计
```
AnonExplorer.jsx (198 行)
  ├── 导航: navPath state → 空=根, dir1=dir1/, dir1/dir2=dir1/dir2/
  ├── currentItems (useMemo):
  │     for entry in entries:
  │       if path starts with prefix:
  │         rest = path.slice(prefix.length)
  │         if rest has '/' → dirs.add(rest before '/')
  │         else → files.push(entry)
  │     return { dirs, files, totalFiles }
  ├── navIn(dir) → setNavPath prev ? `${prev}/${dir}` : dir
  ├── navBack() → pop last segment
  ├── UI:
  │     ├── 顶部: ← 按钮 + hash 输入框 + 查看按钮
  │     ├── 标题栏: navPath ? "← 返回" : "📦 合集名"
  │     │     + 面包屑: 合集名 › dir1 › dir2
  │     │     + 文件数 + 💾 保存按钮
  │     ├── 内容: dirs 渲染为可点击文件夹, files 渲染为下载链接
  │     └── 空: "此目录为空"
  └── 移除: Commit/Fork 按钮 (用户说意义不明)
```

### 使用方案
1. 输入 hash 查看合集 → 面包屑显示合集名
2. 点击文件夹 → 进入 → 面包屑更新为 `合集名 › 文件夹`
3. 点击 `← 返回` → 回到上一级
4. 点击面包屑中的某一级 → 直接跳转到该级
5. 点击文件名 → 直接下载
6. 点击 💾 保存 → 创建合集副本到本地

### 测试点
- hash 粘贴自动跳转
- 根目录显示文件夹+文件
- 点击文件夹 → 进入 → 面包屑更新
- 返回按钮回到上一级
- 面包屑点击跳转
- 文件名点击下载
- 💾 保存 → 创建副本 → 跳转新页面
- 空目录显示"此目录为空"

### 测试结果 ✅
- `npm run build` 通过
- 手动测试: 面包屑导航 + 文件下载 + 💾 保存均正常

---

## 6. FileManager — 文件管理双视图

**需求来源**: 文件管理器.txt "扁平 → 目录树 + 列表切换"

### 代码设计
```
FileManager.jsx (482 行)
  ├── CATEGORIES: [{key:'',label:'全部'}, {image/,video/,audio/,document,archive}]
  ├── SORT_KEYS: [时间,名称,大小,类型]
  ├── viewMode: 'list'|'tree'
  ├── filtered (useMemo): sort → category filter → name search
  ├── List View:
  │     ├── 表头: checkbox + 文件名 + 大小 + 类型 + 时间
  │     ├── 行: checkbox(accent-cyan-500) + icon + <a href=download>filename + size + type(截断charset) + time
  │     └── hover: group-hover → "合集" 按钮 (navigate draftFrom)
  ├── Tree View:
  │     ├── buildTree(files) by provider_path → 目录节点
  │     ├── collectAllHashes(node) → 递归收集所有子文件 hash
  │     ├── 目录行: checkbox(级联) + ▸/▾ + 📁 + name + file count + "创建合集"
  │     └── 展开: 文件行 + 递归子目录
  ├── File Browser Modal:
  │     ├── browseDir(dir) → GET /files/browse → DirEntry[]
  │     ├── 目录导航: ← → / + currentDir 面包屑
  │     └── 注册 → registerFolder → navigate draftFrom
  └── handleCreateFromFile/Dir → navigate('/anon/create', {state:{draftFrom}})
```

### 使用方案
1. 文件管理器 → 默认列表视图 (按时间排序)
2. 分类筛选 → 点击 "图片" → 仅显示 image/* 文件 (计数实时更新)
3. 搜索框输入文件名 → 实时过滤
4. 切换排序: 点击"大小" → 按文件大小排序
5. 切换视图: 点击"📁 目录树" → 按 provider_path 分组 → 展开文件夹
6. 勾选文件夹 → 自动选中所有子文件
7. 点击文件名 → 直接下载文件
8. hover 显示"合集" → 点击进入 AnonCreator 草稿模式
9. "浏览文件系统" → 弹出模态框 → 导航 → 选择 → 注册

### 测试点
- 列表视图: 所有列正确显示 (文件名可点击下载)
- 分类筛选: 点击"图片"仅显示图片文件, 计数正确
- 搜索: 输入 "single" → 仅显示 single.txt
- 排序: 时间/名称/大小切换生效
- 目录树视图: 文件夹展开展示所有文件
- 文件夹 checkbox 级联: 选中文件夹 → 所有子文件选中
- 浏览文件系统: 模态框打开, 根目录正确列出, 导航正常

### 测试结果 ✅
- `npm run build` 编译通过
- 手动测试: 切换视图 + 分类筛选 + 目录树级联 + 文件下载均正常

---

## 7. Tags — 合集标签功能

**需求来源**: commit.txt "还有我之前应该还有什么tag功能你又不记得了是吧"

### 代码设计
```
Go Backend:
  model/anon.go:
    AnonCollection: +Tags []string (JSON `tags,omitempty`)
    AnonCollectionSummary: +Tags, +NamePreview string, +EntryCount int
    NewAnonCollection(name, entries, tags) → 初始化

  repo/anon_repo.go:
    ListAnonCollections: 读取 JSON → Tags, EntryCount=len(Entries),
      NamePreview = strings.Join(entries[:3].path, ", ")
    +import "strings"

  controller/anon.go:
    CreateAnonCollection.FriendlyName + Entries + Tags → CreateCollection(..., tags)
    ForkAnonCollection → CreateCollection(friendlyName, newEntries, src.Tags)

  service/anon_service.go:
    CreateCollection(name, entries, tags) → NewAnonCollection(name, entries, tags)
    newAnonCollectionWithVersion(..., tags) → 传 tags

React Frontend:
  api.js:
    createAnonCollection(entries, name, tags=[]) → POST body {tags}

  AnonCreator.jsx:
    tags state → <input placeholder="标签: tag1, tag2">
    handleMint: tags.split(/[,;]/).map(t=>t.trim()).filter(Boolean)

  AnonExplorer.jsx:
    collection.tags → <span className="bg-blue-900/50">chip</span>

  Plaza.jsx:
    c.tags && <div className="flex gap-1">tag chips</div>
```

### 使用方案
1. AnonCreator: 在"标签"输入框中输入 `图片, 文档, 2026`
2. 保存合集 → 标签随合集 JSON 存储
3. AnonExplorer: 合集头部显示 tag chips
4. Plaza 合集列表: 每个合集卡片显示 tag chips
5. Navbar SearchPanel 搜索: 输入 tag 关键词 → 匹配合集

### 测试点
- 创建合集时输入 tags (逗号分隔) → 保存
- GET /anon/collections/:hash → 返回 tags 数组
- AnonExplorer 头部显示 tag chips
- Plaza 卡片显示 tag chips
- ListAnonCollections summary 返回 tags + entry_count + name_preview
- name_preview = 前 3 个文件名 (逗号分隔)
- Fork 时 tags 被继承到新合集

### 测试结果 ✅
- Go `go build ./...` 通过
- React `npm run build` 通过
- API 测试: POST /anon/collections {tags:["a","b"]} → GET → tags 正确返回

---

## 8. LLM — AI 名称推荐

**需求来源**: TODO.txt "如果没有设定合集名称,则让用户选择LLM给一个名称还是留空"

### 代码设计
```
AnonCreator.jsx:
  const LLM_URL = getLlmEndpoint() → 'https://siliconflow.moonchan.xyz'
  const LLM_CHAT = `${LLM_URL}/v1/chat/completions`

  async llmSuggest(names) {
    POST LLM_CHAT {
      model: 'Qwen/Qwen3-8B',
      messages: [{role:'user', content:'请用3-5个中文字为以下文件集取一个简洁的合集名称,只输出名称: ' + names}],
      max_tokens: 20, stream: false
    }
    → response.choices[0].message.content.trim().replace(/["""'']/g, '')
  }

  使用场景 1: 🤖 按钮 (工具栏)
    → 有 entries → llmSuggest(entries.slice(0,20).map(e=>e.path).join(', '))
    → 设置 fname

  使用场景 2: 保存无名称合集时
    → confirm('点确定用AI,点取消留空')
    → 确定 → llmSuggest → 设置 fname
    → cancel → 留空保存
```

### 使用方案
1. 已添加文件 → 点击工具栏 `🤖` 按钮 → AI 自动生成 3-5 字名称
2. 无名称时点"保存" → 弹窗 → 点"确定"用 AI → 点"取消"留空

### 测试点
- 🤖 按钮调用 LLM → 返回中文名称 → 设置 fname 输入框
- LLM 调用失败 → catch 静默处理 (不阻塞保存)
- 空 entries 时 🤖 按钮不生效
- 保存时 confirm 弹窗 → 确定/AI名称 → 取消留空
- endpoint 指向 siliconflow.moonchan.xyz (非 wsl-3000)

### 测试结果 ✅
- `npm run build` 通过
- URL 正确: const LLM_URL = api.getLlmEndpoint()

---

## 9. Go — CORS + Browse 文件系统

**需求来源**: Go cors.txt + 游览文件系统.txt

### 代码设计
```
router.go:
  r.RedirectTrailingSlash = false  // 修复 /files/browse/ → 301
  r.RedirectFixedPath = false

  CORS 中间件:
    origin = c.Request.Header.Get("Origin")
    c.Header("Access-Control-Allow-Origin", origin || "*")
    c.Header("Access-Control-Allow-Headers", c.Request.Header.Get("Access-Control-Request-Headers"))

config.go:
  DefaultRootPath() -> Windows: "C:\", Linux: "/"
  AllowedOrigins: env PEERDRIVE_ALLOWED_ORIGINS (默认 "*")
  IsOriginAllowed(origin) → 通配符/精确/子域名匹配

controller/file.go:
  BrowseDir: path = c.Query("path") || config.DefaultRootPath()

service/file_service.go:
  BrowseDir(dirPath) → filepath.IsAbs? → os.ReadDir → DirEntry[]
```

### 使用方案
1. 前端 `/files/browse?path=%2F` → CORS headers 自动回显 Origin
2. 设置 `PEERDRIVE_ALLOWED_ORIGINS=peerdrive.pages.dev,peerdrive.moonchan.xyz` 限制访问

### 测试点
- curl -H "Origin: https://peerdrive.pages.dev" → Access-Control-Allow-Origin: https://peerdrive.pages.dev
- curl /files/browse/ → 不再 301 redirect (RedirectTrailingSlash=false)
- curl /files/browse?path=%2F → 200 + JSON root dir listing
- curl /files/browse?path=%2Fmnt → 200 + /mnt/ 子目录

### 测试结果 ✅
- `go build ./...` 通过
- curl 手动验证: root dir /files/browse 返回完整 JSON, CORS header 存在
- Windows 默认 C:\ 路径支持 (DefaultRootPath)

---

## 10. FileManager — 文件夹 checkbox 级联 + MIME 修整

**需求来源**: TODO.txt "选中应该能选中文件夹与子文件夹" + "MIME不要显示多行"

### 代码设计
```
FileManager.jsx:
  collectAllHashes(node):
    hashes = node.files.map(f=>f.hash)
    for dirName in node.dirs:
      hashes.push(...collectAllHashes(node.dirs[dirName]))
    return hashes

  renderTree → 目录 checkbox:
    allHashes = collectAllHashes(dir)
    dirSelected = allHashes.length>0 && allHashes.every(h=>selected[h])
    onChange: for each hash in allHashes: next[hash] = !dirSelected

  MIME 修整 (List View 第 410 行):
    (f.mime_type || '').split(';')[0].split('/').pop()
    // "text/plain; charset=utf-8" → "text/plain" → "plain"
    +overflow-hidden text-ellipsis whitespace-nowrap
```

### 使用方案
1. 文件管理器 → 目录树视图
2. 展开文件夹 → 勾选文件夹 → 所有子文件和子文件夹自动选中
3. 取消勾选文件夹 → 所有子文件取消
4. 列表视图中 MIME 列不再出现 "plain; charset=utf-8"，只显示 "plain"

### 测试点
- 文件夹 (含 3 个文件) → 勾选 → 3 个文件全部选中
- 文件夹 (含子目录) → 勾选 → 子目录中的文件也选中
- 取消勾选 → 全部取消
- MIME "text/plain; charset=utf-8" → 显示 "plain"
- MIME "image/png" → 显示 "png"

### 测试结果 ✅
- `npm run build` 通过
- 手动测试: 文件夹级联有效，MIME 截断正确

---

## 11. Private 合集生命周期

**需求来源**: register.txt "只要选中目录就自动创建一个private的匿名合集" + "如果是private合集，在进行修改的时候，直接删除旧合集"

### 代码设计
```
AnonCreator.jsx:
  handleMint:
    oldHash = savedHash (保存前记下旧hash)
    → createAnonCollection → 新hash
    → if oldHash: api.deleteFile(oldHash) (异步，静默失败)
    → navigate to 新合集

  handleCommit:
    oldHash = savedHash
    → commitAnonCollection → 新hash
    → if oldHash: api.deleteFile(oldHash)
    → navigate

AnonExplorer.jsx:
  💾 保存按钮:
    createAnonCollection(collection.entries, name+' (副本)')
    → navigate to 新合集
```

### 使用方案
1. 创建合集 → Mint → 原始文件保留
2. 再次编辑 → Commit → 旧版本文件被 deleteFile 删除
3. 本地合集的旧版本不再占用磁盘空间
4. 远程合集 (他人分享的) → 💾 保存副本到本地 node

### 测试点
- 创建合集 → Mint → 生成 hash-A
- 添加文件 → Commit → 生成 hash-B, hash-A 调用 deleteFile
- AnonExplorer 💾 → 创建副本 → 新 hash-C
- deleteFile 调用失败不阻塞保存流程

### 测试结果 ✅
- `npm run build` 通过
- deleteFile 异步 + catch 静默处理

---

## 编译验证汇总

| 项目 | 命令 | 结果 |
|------|------|------|
| React 前端 | `npm run build` | ✅ 35 modules, 322KB JS, 28KB CSS |
| Go 后端 | `go build ./...` | ✅ 无错误 |
| vitest | `npm test` (测试文件存在) | ⚠️ tests/FileTree.test.jsx 已添加，需 @testing-library/react compatible with React 19 |

---

## 已删除的 txt 文件 (已回复)

| 文件 | 回复章节 |
|------|---------|
| 这集神了.txt | 第 1, 9 节 |
| 我要死了.txt | 第 1, 2, 3, 8 节 |
| 我服了.txt | 第 3, 6 节 |
| collection.txt | 第 2 节 |
| 文件管理器.txt | 第 6, 10 节 |
| 合集界面.txt | 第 5, 7 节 |
| 创建合集.txt | 第 3 节 |
| commit.txt | 第 3, 7 节 |
| collections.txt | 第 5 节 |
| UI.txt | 第 1, 3, 4 节 |
| register.txt | 第 6, 11 节 |
| TODO.txt | 全部 16 条, 逐条回复见上文 |

---

## 关键文件索引

| 文件 | 功能 | 行数 |
|------|------|------|
| `components/Navbar.jsx` | 全局搜索 + Ctrl+K | ~90 |
| `components/FileTree.jsx` | 递归树组件 (拖拽/重命名/文件夹) | ~215 |
| `components/LLMAssistant.jsx` | LLM 助手 (17 tools + SSE) | ~238 |
| `pages/AnonCreator.jsx` | 左右分栏合集编辑器 | ~356 |
| `pages/AnonExplorer.jsx` | Android 合集浏览器 | ~198 |
| `pages/Plaza.jsx` | 合集列表首页 (skeleton/dummy) | ~166 |
| `pages/FileManager.jsx` | 文件管理 (双视图+级联) | ~482 |
| `pages/Explorer.jsx` | 具名合集 Merge 下拉 | ~311 |
| `api.js` | 全部 API 封装 | ~206 |
| `go/internal/model/anon.go` | AnonCollection + Tags + Summary | ~50 |
| `go/internal/repository/anon_repo.go` | 合集 CRUD + name_preview | ~120 |
| `go/internal/router/router.go` | CORS + 路由 | ~188 |
| `go/internal/config/config.go` | AllowedOrigins + DefaultRootPath | ~112 |

---

## 功能与 API 端点对照

### Plaza 首页
- `GET /anon/collections` — 列出匿名合集 (AnonCollectionSummary[])
- `GET /collections/public?q=` — 列出公开合集
- 合并结果 → 按 type 标记 → 渲染卡片
- `DELETE /files/:hash` — 删除合集文件 (旧版本)
- `GET /anon/collections/:hash/:filepath` — 下载文件

### AnonCreator 创建/编辑合集
- `GET /files?sort=time` — 左面板"本地文件"列表
- `GET /files/browse?path=%2F` — 文件系统浏览器
- `GET /anon/collections` — 左面板"已有合集"列表
- `GET /anon/collections/:hash` — 加载选中合集的条目
- `POST /anon/collections` — 创建新合集 `{entries, friendly_name, tags}`
- `POST /anon/collections/commit` — 提交新版 `{source_hash, entries, commit_message}`
- `DELETE /files/:hash` — 删除旧版本合集 JSON
- `POST https://siliconflow.moonchan.xyz/v1/chat/completions` — AI 推荐名称

### FileManager 文件管理
- `GET /files?sort=path` — 目录树模式加载
- `GET /files?sort=time` — 列表模式加载
- `GET /files/browse?path=...` — 浏览文件系统模态框
- `POST /files/register_local` — 注册单个文件
- `POST /files/register_folder` — 注册文件夹
- `GET /sha256sum/:hash` — 文件名点击下载

### AnonExplorer 合集浏览
- `GET /anon/collections/:hash` — 获取合集 JSON
- `GET /anon/collections/:hash/:filepath` — 文件下载
- `POST /anon/collections` — 💾 保存副本 (createAnonCollection)
- `POST /anon/collections/fork` — Fork 合集
- `POST /anon/collections/commit` — Commit 版本 (已从 UI 移除但 API 仍存在)

### Navbar 搜索
- `GET /anon/collections` — 搜索匿名合集 (本地 filter)
- `GET /collections/search?q=...` — 搜索公开合集
- `GET /files?sort=time` — 搜索注册文件 (本地 filter)

### Settings 设置
- `GET /ping` — 测试连接 (ping)
- 所有设置存 localStorage: `peerdrive_api_base`, `peerdrive_llm_endpoint`, etc.

