# Peerdrive Frontend Documentation

## 1. Project Overview

**Peerdrive React Frontend** is a decentralized file collection management SPA deployed to **Cloudflare Pages** (auto-deploy on push to `frontend` branch).

| Aspect | Detail |
|--------|--------|
| **Framework** | Vite 8 + React 19 (JSX) |
| **Styling** | Tailwind CSS 3.4 |
| **Routing** | React Router DOM 7 (`BrowserRouter`) |
| **State** | React Context (`AppContext` + `PageContext`) |
| **Testing** | Vitest 4 + @testing-library/react + happy-dom |
| **Node** | >= 22.12 or >= 20.19 |
| **LLM Integration** | OpenAI-compatible chat/completions (17 function-calling tools, SSE streaming) |
| **Deployment** | Cloudflare Pages — `public/_redirects` required for SPA fallback |

### Build & Run

```bash
npm run dev        # Vite dev server
npm run build      # Production build → dist/
npm run preview    # Preview production build
npm test           # Run Vitest test suite
```

### Route Map

| Route | Page | Description |
|-------|------|-------------|
| `/` | Plaza | Collection discovery (anon + public) |
| `/files` | FileManager | Registered file browser, categories, tree view |
| `/:username/:collName` | Explorer | User collection detail, commit, merge, sync |
| `/anon/create` | AnonCreator | Split-pane drag-and-drop collection builder |
| `/anon/collections/:hash` | AnonExplorer | View anonymous collection by SHA256 hash |
| `/anon` | AnonExplorer | Anonymous collection explorer (empty state) |
| `/settings` | Settings | API endpoint, auth, LLM configuration |
| `/p2p` | P2PPanel | P2P dual-stack control (IPFS + BT unified) |
| `/p2p/ipfs` | IPFSPanel | IPFS / libp2p network panel |
| `/p2p/bt` | BTPanel | BT DHT network panel |
| `/p2p/bt/controller` | BTController | BT downloader with pause/resume/remove |
| `/p2p/dashboard` | P2PDashboard | P2P network dashboard (peers, topology, latency) |
| `/p2p/topology` | P2PTopology | P2P network topology visualization |
| `/p2p/dht` | DHTExplorer | Unified DHT query panel (IPFS + BT dual lookup) |

---

## 2. Key Components

### 2.1 Navbar (with SearchPanel)

| Property | Detail |
|----------|--------|
| **File** | `src/components/Navbar.jsx` (251 lines) |
| **Purpose** | Top navigation bar with auth indicator, links, global search overlay |
| **Sub-components** | `SearchPanel` (inline in same file, lines 5-155), Auth status indicator (inline) |

**Props:** None (uses `useNavigate` internally)

**State:**
- `searchOpen: boolean` — toggles the search modal overlay
- SearchPanel holds: `q` (query string), `results { anon, public, files }`, `loading`, `activeIdx`

**API Endpoints Used:**
- `GET /anon/collections` (via `listAnonCollections`)
- `GET /collections/search?q=` (via `searchCollections`)
- `GET /files?sort=time` (via `listFiles`)

**Keyboard Shortcuts:**
- `Ctrl+K` / `Cmd+K` — open search
- `/` (when focused on body) — open search
- `↑↓` — navigate results
- `Enter` — open selected result
- `Esc` — close search

**Known Issues:**
- SearchPanel is defined inline in Navbar.jsx (not a separate file), making it untestable in isolation
- `useNavigate()` closure captured in `useEffect` — will be stale if Navbar re-renders should the navigate function reference change

---

### 2.2 FileTree

| Property | Detail |
|----------|--------|
| **File** | `src/components/FileTree.jsx` (332 lines) |
| **Purpose** | Recursive directory tree renderer with drag-drop, rename, move-to, context menu, new folder |
| **Exports** | `FileTree` (default), `buildFlatTree` (named) |

**Props:**
```js
{
  entries: Array<{ path: string, hash: string, size?: number, mime_type?: string }>,
  entryActions: {
    onRemove?: (node) => void,
    onRename?: (oldPath, newPath) => void,
    onNewFolder?: (name) => void,
    onDrop?: (data: { hash, path, name, mime_type, size, targetDir }) => void
  }
}
```

**State:**
- `expanded: Set<string>` — paths of expanded directories
- `renaming: string | null` — path currently being renamed
- `dragOverPath: string | null` — highlight target for drop
- `showNewFolder: boolean`, `newFolderName: string` — inline folder creation
- `showMoveModal: { path, isDir } | null` — "move to" modal for moving entries between directories
- `contextMenu: { x, y, node } | null` — right-click context menu (rename, move, delete)

**Drag MIME Types Used:**
- `application/peerdrive-path` — for directory drag
- `application/peerdrive-entry` — for file entry drag (JSON: `{ hash, path, name, mime_type, size }`)
- `application/peerdrive-file` — simplified file drag (JSON: `{ hash, path, name, ... }`)
- `text/plain` — fallback for cross-browser compatibility

**Internal Helpers:**
- `buildTree(entries)` — converts flat `{path, hash}` array into nested tree structure
- `fileIcon(mime)` — returns emoji based on MIME type
- `fmtSize(bytes)` — human-readable file size

**Known Issues:**
- **CRASH**: `buildTree` accesses `node.files` on non-directory nodes (line 113: `node.files.length`). If a node has `_files` empty and `_children` also empty after the flatten logic, the `files` property may be `undefined`, causing `node.files.length` to throw
- Double-click rename only works on files inside expanded directories, not on top-level files
- No `onDragEnd` cleanup — `dragOverPath` may stick if user drags outside the tree

---

### 2.3 LLMAssistant

| Property | Detail |
|----------|--------|
| **File** | `src/components/LLMAssistant.jsx` (623 lines) |
| **Purpose** | Floating AI chatbot with function-calling, SSE streaming, tool execution |

**Props:** None (uses Context + React Router hooks internally)

**State:**
- `open: boolean` — toggle chat window
- `messages: [{role, content}]` — display messages array
- `input: string` — user input text
- `streaming: boolean`, `streamText: string`, `toolStatus: string`
- `abortRef` — AbortController for stream cancellation

**System Prompt:** Includes current page context (`route`, `timestamp`, `pageData` from `PageContext`)

**17 Tools (Function Calling):**

| # | Tool Name | Description | API Called |
|---|-----------|-------------|------------|
| 1 | `navigate_to` | Navigate browser to a PeerDrive page | Router `navigate(route)` |
| 2 | `get_node_info` | Node status + ping | `GET /ping`, `GET /p2p/node` |
| 3 | `list_collections` | List user or anonymous collections | `GET /collections/:user` or `GET /anon/collections` |
| 4 | `list_public_collections` | Browse public collections | `GET /collections/public` |
| 5 | `search_collections` | Search by keyword | `GET /collections/search?q=` |
| 6 | `create_collection` | Create user collection | `POST /collections` |
| 7 | `get_collection_info` | Get collection details + entries | `GET /collections/:user/:coll` |
| 8 | `add_file_to_collection` | Add entry by hash | `POST /collections/:user/:coll/entries` |
| 9 | `commit_collection` | Commit with message | `POST /collections/:user/:coll/commit` |
| 10 | `fork_collection` | Fork from another user | `POST /actions/fork` |
| 11 | `get_version_log` | View commit history | `GET /collections/:user/:coll/log` |
| 12 | `register_local_file` | Register a local file | `POST /files/register_local` |
| 13 | `register_folder` | Register a folder | `POST /files/register_folder` |
| 14 | `get_tasks` | List background tasks | `GET /tasks` |
| 15 | `get_p2p_status` | P2P network status | `GET /p2p/status` |
| 16 | `create_anon_collection` | Create anonymous collection | `POST /anon/collections` |
| 17 | `verify_file` | Verify file by SHA256 | `GET /files/verify/:hash` |

**SSE Streaming Flow:**
1. POST to `{endpoint}/v1/chat/completions` with `stream: true`
2. Read response body via `ReadableStream.getReader()`
3. Parse `data:` SSE lines, extract `delta.content` for text and `delta.tool_calls` for function calls
4. Accumulate tool call arguments across chunks (OpenAI streams them incrementally by `tc.index`)
5. Multi-turn loop: up to 5 tool-calling rounds (`MAX_TURNS = 5`)

**LLM Configuration (localStorage keys):**
- `peerdrive_llm_endpoint` — default: `https://siliconflow.moonchan.xyz`
- `peerdrive_llm_model` — default: `Qwen/Qwen3-8B`
- `peerdrive_llm_apikey` — optional for free models
- `peerdrive_llm_body_template` — JSON template for request body

**Known Issues:**
- Must use `siliconflow.moonchan.xyz` proxy (NOT `wsl-3000`) — the LLM endpoint is separate from the API endpoint
- `buildContext()` creates a new object every render; dependencies don't capture the latest `pageContext` value in multi-turn loops
- Tool call execution errors are swallowed as strings — user sees `Error: ...` as assistant response
- `useEffect` capture of `location` and `pageContext` may be stale in `buildContext` during streaming

---

### 2.4 SearchPanel

| Property | Detail |
|----------|--------|
| **File** | Inline in `src/components/Navbar.jsx` (lines 5-155) |
| **Purpose** | Command-palette-style global search overlay |

**Props:** `{ open: boolean, onClose: () => void }`

**State:** `q, results { anon, public, files }, loading, activeIdx`

**Search Sources:**
1. **Anonymous collections** — filtered client-side by `friendly_name` or `hash` (max 3 results)
2. **Public collections** — server-side search via `GET /collections/search?q=`
3. **Registered files** — filtered client-side by `filename` (max 3 results)

**Debounce:** 200ms via `setTimeout`

**Known Issues:**
- Three parallel searches on every keystroke after debounce; the `listFiles('time')` call fetches /files for every query
- No error boundary — if one fetch throws, all results are silently cleared

---

## 3. Key Pages

### 3.1 Plaza (Collection Discovery)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/Plaza.jsx` (129 lines) |
| **Route** | `/` |

**API Endpoints:**
- `GET /anon/collections` — list all anonymous collections
- `GET /collections/public?q=` — list public collections

**State:**
- `collections: Array` — merged list of anon + public collections
- `loading: boolean` — shows skeleton cards while loading

**Features:**
- **Skeleton loading**: 8 animated placeholder cards during fetch
- **Dummy data**: When both anon and public return empty, shows 2 hardcoded demo collections (`demo-1` demo image set, `demo-2` demo document set)
- **Grid layout**: Responsive 1-4 column grid (`grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4`)
- Click card navigates to `/anon/collections/:hash` or `/:username/:collection_name`
- **"+ 创建合集"** button navigates to `/anon/create`

**Data Handling:**
- Merges `anon` array (mapped with `_type: 'anon'`) and `pub.collections` or `pub.data` (mapped with `_type: 'public'`)
- Each card shows: icon, date, name, username, tags

**Known Issues:**
- Dummy data has no actual link (demo cards are non-clickable, `cursor-default opacity-80`)
- If `listAnonCollections()` returns non-empty but `listPublicCollections()` throws, the error is silently caught and collections list will be incomplete

---

### 3.2 AnonCreator (Collection Builder)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/AnonCreator.jsx` (357 lines) |
| **Route** | `/anon/create` |

**API Endpoints:**
- `GET /files?sort=` — list registered files (source panel)
- `GET /anon/collections` — list existing anon collections (source panel)
- `GET /anon/collections/:hash` — load collection as source
- `POST /anon/collections` — save new collection
- `POST /anon/collections/commit` — commit changes to existing
- LLM chat: `POST {llm_endpoint}/v1/chat/completions` — AI naming suggestion

**State (20+ state variables):**
- `split: number` — percentage split between source panel and target panel
- `files, fLoading, sort, search, typeF` — source file list state
- `collections, collSource` — existing collection source
- `srcTab: 'local' | 'collection'` — which source tab is active
- `localDirPath` — current directory in file browser
- `entries: [{hash, path, mime_type, size}]` — current working set
- `fname, tags` — collection metadata
- `openHash, savedHash` — tracking whether collection is new/modified
- `saving` — save-in-progress flag

**UI Layout:**
- **Split pane**: Left panel (file/collection source) | Resizable divider | Right panel (FileTree of entries + save/commit buttons)
- Drag divider: mouse-down starts tracking, `mousemove`/`mouseup` handlers adjust `split` (20%-80% range)
- Left panel has tabs: "本地文件" (with sort + type filter + directory browser) and "已有合集" (browse + load)

**Entry States:**
- **Draft (new)**: No `savedHash` → button shows "未保存" badge, green "保存" button
- **Modified**: Has `savedHash` and entries changed → "已修改" badge, blue "Commit" button appears
- **Pristine**: Has `savedHash` and no changes → "Commit" button still shown

**Drag Support:**
- Source files: `text/plain` + `application/peerdrive-file` MIME types
- Target FileTree handles drops via `onDrop` entryAction

**LLM Naming:**
- `llmSuggest(names)` — sends `POST /v1/chat/completions` with `Qwen/Qwen3-8B`, prompt asks for 3-5 Chinese character name
- Trigger: "🤖" button in toolbar, or auto-triggered when saving without a name (via `confirm()` dialog)

**Navigation State Handling (draft mode):**
- `navState.forkFrom` — pre-fills entries from forked collection
- `navState.draftFrom` — pre-fills entries from file manager selection
- `navState.editFrom` — loads existing collection for editing
- After processing, calls `nav('/anon/create', { replace: true })` to clear state

**Known Issues:**
- Click debounce (500ms) on `addEntry` — rapid clicks are ignored
- `useEffect` for navState has empty deps array (intentional, runs once) but reads `nav` from closure
- `llmSuggest` uses a hardcoded model name `'Qwen/Qwen3-8B'` instead of the configured LLM model from settings

---

### 3.3 AnonExplorer (Collection Viewer)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/AnonExplorer.jsx` (195 lines) |
| **Route** | `/anon/collections/:hash`, `/anon` |

**API Endpoints:**
- `GET /anon/collections/:hash` — fetch collection by hash

**State:**
- `inputVal: string` — search input value
- `searchHash: string` — resolved hash to fetch
- `collection: object | null` — fetched collection data
- `loading, error` — fetch status
- `navPath: string` — current directory path for breadcrumb navigation

**Features:**
- **Hash input**: Text field accepts full SHA256 hash; auto-extracts via regex `/\b([a-f0-9]{64})\b/i`
- **Paste support**: Automatically extracts hash from pasted text
- **Breadcrumb navigation**: Click directory names to navigate, `›` separators
- **Directory browser**: `useMemo` computes `dirs` and `files` for current `navPath`
- **"💾 保存" button**: Creates a copy of the current collection with " (副本)" suffix
- **Fork/Edit buttons**: Navigate to `AnonCreator` with state (`forkFrom` / `editFrom`)
- File entries are clickable download links: `{API_BASE}/anon/collections/{hash}/{filepath}`

**Known Issues:**
- No loading skeleton — shows centered "加载中..." text
- Hash extraction regex can match a hash embedded in a longer string (e.g., URL fragments)

---

### 3.4 FileManager

| Property | Detail |
|----------|--------|
| **File** | `src/pages/FileManager.jsx` (472 lines) |
| **Route** | `/files` |

**API Endpoints:**
- `GET /files?sort=path` — load all registered files
- `GET /files/browse?path=` — browse server filesystem
- `POST /files/register_folder` — register folder contents
- `POST /files/register_local` — register individual file (via browse modal)

**State:**
- `files: Array` — all registered files
- `loading, search, sortBy, category, viewMode` — list state
- `selected: { [hash]: boolean }` — checkbox selection state
- `expanded: { [dirname]: boolean }` — tree view expansion
- `showFileBrowser: boolean` — browse modal open/close
- `currentDir, dirEntries, selectedFiles, browseLoading` — browse modal state
- `regTarget: 'collection' | 'file'` — post-register action

**View Modes:**
1. **List view** (`viewMode === 'list'`): Table with checkboxes, icon, filename (downloadable link), size, type, timestamp
2. **Tree view** (`viewMode === 'tree'`): Directory tree built from `provider_path` field, with cascade checkboxes

**Features:**
- **Category chips**: 全部, 图片, 视频, 音频, 文档, 压缩包 — with counts
- **Sort buttons**: 时间, 名称, 大小, 类型
- **Cascade checkboxes**: In tree view, checking a directory selects/deselects all descendant files
- **Browse modal**: File system browser for registering new files from server paths; supports directory navigation with breadcrumb
- **Create collection from selection**: Selected files → navigate to AnonCreator with `draftFrom` state
- **Per-file "合集" button**: Hover on a file row → quick-create a collection from single file

**Checkbox Styling:**
```css
accent-cyan-500  /* Required for visibility on dark bg-gray-800/900 */
```
- Standard `accent-blue-*` is invisible on dark backgrounds; must use `accent-cyan-500`

**Known Issues:**
- `GET /files?sort=path` always loaded; sort/filter is done client-side. The `/files` endpoint's `sort` parameter may return inconsistent ordering
- Tree `buildTree` uses `provider_path` for directory hierarchy, but `provider_path` may contain full server paths (e.g., `/storage/...`), causing unexpected root-level nesting
- Register modal closes on success even if `regTarget === 'file'` (no navigation happens but modal closes)

---

### 3.5 Explorer (User Collection Detail)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/Explorer.jsx` (311 lines) |
| **Route** | `/:username/:collName` |

**API Endpoints:**
- `GET /collections/:username/:coll` — load collection entries
- `POST /collections/:username/:coll/entries` — add entry (via upload)
- `POST /collections/:username/:coll/commit` — commit version
- `POST /actions/merge` — merge from another collection
- `DELETE /collections/:username/:coll/entries/:path` — remove entry
- `POST /files/upload` — upload file (multipart)
- `GET /anon/collections` — list anon collections (merge source)
- `GET /collections/public` — list public collections (merge source)
- `POST /local/save` — save to local filesystem
- `GET /local/status/:hash` — check sync status

**State:**
- `entries: [{path, file_hash, ...}]` — current collection entries
- `commitMsg: string` — commit message input
- `showMergeModal, mergeSrc {user, coll, strategy}`, `mergeCollections, mergeCustom`
- `showSyncModal, syncConfig {path, include, exclude}`, `syncStatus`
- `refreshTrigger: number` — incremented to re-fetch after mutations

**Features:**
- **Upload**: File input → upload → auto-add entry to collection
- **Commit**: Text input + "Commit" button → `POST /collections/:user/:coll/commit`
- **Merge modal**: Dropdown of anon + public collections, or custom `user/coll` inputs; strategy: `ours` / `theirs` / `manual`
- **Save to local modal**: Absolute path + include/exclude glob patterns → `POST /local/save`
- **VersionLog sidebar**: Right panel showing commit history with rollback capability
- **Entry table**: path, hash (truncated), download link, remove button

**Known Issues:**
- Uploaded file is auto-added to collection without confirmation
- Merge sources list both anon collections (by hash) and user collections — anonymous collections in the merge dropdown may not have associated entries in the user's collection DB
- `downloadFileByPath` uses `api.getUserFileDownloadUrl` which constructs `{API_BASE}/{username}/{collName}/{filepath}` — assumes URL path encoding is handled by the browser
- Rollback in VersionLog triggers `window.location.reload()`, losing unsaved changes without warning

---

### 3.6 Settings

| Property | Detail |
|----------|--------|
| **File** | `src/pages/Settings.jsx` (336 lines) |
| **Route** | `/settings` |

**Props:** `{ dataConsent: boolean, setDataConsent: (v) => void }`

**API Endpoints:**
- `GET /ping` — test endpoint connectivity
- `GET /p2p/node` — get node info
- `GET /files/verify/:hash` — file hash validation test

**Configuration Sections:**

| Section | localStorage Key | Default |
|---------|-----------------|---------|
| API Base URL | `peerdrive_api_base` | `https://wsl-3000.moonchan.xyz` |
| Auth Key | `peerdrive_auth_key` | `''` |
| LLM Endpoint | `peerdrive_llm_endpoint` | `https://siliconflow.moonchan.xyz` |
| LLM Model | `peerdrive_llm_model` | `Qwen/Qwen3-8B` |
| LLM API Key | `peerdrive_llm_apikey` | `''` |
| LLM Body Template | `peerdrive_llm_body_template` | JSON with model, stream, max_tokens, temperature |
| Data Consent | `peerdrive_data_consent` | `false` |
| Username | `peerdrive_username` | `''` |

**Features:**
- **Live ping test**: On mount and on API base change, tests connectivity; green/red dot indicator
- **Node info display**: Raw JSON of P2P node status
- **Auth key**: Password-masked input with show/hide toggle; can write to URL hash via `#key=xxx`
- **LLM model dropdown**: Pre-populated with 13 free SiliconFlow models
- **Body JSON template**: Editable textarea, replaces model + messages at request time
- **File hash verification**: Manual SHA256 input + verify button
- **Data consent**: Checkbox with upload confirmation

**Known Issues:**
- Settings saved to localStorage immediately; no "discard changes" capability after save
- Auth key stored in localStorage as plaintext

---

### 3.7 P2P Network Pages

Seven P2P-related pages are accessible from the Navbar P2P dropdown or via direct routes.

#### P2PPanel (`/p2p`)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/P2PPanel.jsx` |
| **Purpose** | Dual-stack overview — unified view of IPFS (libp2p) and BT (Mainline) DHT status, peer counts, connection stats, and recent events |

#### IPFSPanel (`/p2p/ipfs`)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/IPFSPanel.jsx` |
| **Purpose** | IPFS/libp2p network panel — toggle IPFS compat mode, view DHT peers, CID lookup, Bitswap blockstore stats |
| **API** | `GET /p2p/ipfs`, `POST /p2p/ipfs/toggle` |

#### BTPanel (`/p2p/bt`)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/BTPanel.jsx` |
| **Purpose** | BT DHT network panel — view Mainline DHT peers, infohash operations, BEP44 data store |
| **API** | `POST /p2p/bt/announce`, `POST /p2p/bt/find`, `GET /p2p/bt/bep51/sample` |

#### BTController (`/p2p/bt/controller`)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/BTController.jsx` |
| **Purpose** | BT download manager — add torrent/magnet, pause/resume/remove downloads, progress tracking |

#### P2PDashboard (`/p2p/dashboard`)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/P2PDashboard.jsx` |
| **Purpose** | Network dashboard — connected peers table, latency ping, connection stats, transport types |

#### P2PTopology (`/p2p/topology`)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/P2PTopology.jsx` |
| **Purpose** | Network topology visualization — graph view of peer connections and quality metrics |

#### DHTExplorer (`/p2p/dht`)

| Property | Detail |
|----------|--------|
| **File** | `src/pages/DHTExplorer.jsx` |
| **Purpose** | Unified DHT query panel — enter SHA256/InfoHash/CID, simultaneously query IPFS DHT + BT DHT, display results side-by-side; BEP51 infohash sampling for content discovery |
| **API** | `POST /p2p/dual/find`, `GET /p2p/bt/bep51/sample` |
- LLM body template must be valid JSON; parsing failure silently falls back to a minimal body

---

## 4. API Overview

Default API base: `https://wsl-3000.moonchan.xyz` (configurable in Settings)

### 4.1 File Operations

| Method | Path | Body | Response | Used By |
|--------|------|------|----------|---------|
| `GET` | `/files?sort={time\|name\|path\|type\|size}` | — | `[{filename, hash, size, mime_type, provider_path, created_at}]` | AnonCreator, FileManager, Navbar |
| `GET` | `/files/browse?path={dirPath}` | — | `[{name, path, is_dir, size}]` | FileManager |
| `GET` | `/files/verify/:hash` | — | `{type, size, ...}` | Settings, LLMAssistant |
| `GET` | `/sha256sum/:hash` | — | Binary file download | AnonCreator (download links) |
| `POST` | `/files/upload` | `multipart/form-data` (field: `file`) | `{hash, filename}` | Explorer |
| `POST` | `/files/register_local` | `{path, filename}` | `{hash, filename}` | FileManager, LLMAssistant |
| `POST` | `/files/register_folder` | `{folder_path}` | `{registered: [{filename, hash}]}` | FileManager, LLMAssistant |
| `DELETE` | `/files/:hash` | — | — | AnonCreator (cleanup old hash) |

### 4.2 Anonymous Collections

| Method | Path | Body | Response | Used By |
|--------|------|------|----------|---------|
| `GET` | `/anon/collections` | — | `[{hash, friendly_name, version, entry_count, created_at, tags, entries}]` | Plaza, Navbar, AnonCreator, Explorer |
| `GET` | `/anon/collections/:hash` | — | `{hash, friendly_name, version, created_at, entries: [{path, hash, mime_type, size}]}` | AnonExplorer, AnonCreator, CollectionBuilder |
| `GET` | `/anon/collections/:hash/:filepath` | — | File download | AnonExplorer, CollectionBuilder |
| `POST` | `/anon/collections` | `{entries: [{path, hash}], friendly_name, tags}` | `{hash}` | AnonCreator, AnonExplorer, LLMAssistant |
| `POST` | `/anon/collections/commit` | `{source_hash, entries, commit_message}` | `{hash}` | AnonCreator |
| `POST` | `/anon/collections/fork` | `{source_hash, add_entries, remove_paths, friendly_name}` | `{hash}` | CollectionBuilder |

### 4.3 User Collections

| Method | Path | Body | Response | Used By |
|--------|------|------|----------|---------|
| `GET` | `/collections/public?q={keyword}` | — | `{collections: [{username, collection_name, ...}]}` | Plaza, Navbar, Explorer |
| `GET` | `/collections/search?q={keyword}` | — | `{collections or data: [...]}` | Navbar, LLMAssistant |
| `GET` | `/collections/:username` | — | `[{collection_name, ...}]` | LLMAssistant |
| `GET` | `/collections/:username/:coll` | — | `{entries: [{path, file_hash}], current_hash}` | Explorer |
| `GET` | `/:username/:coll/:filepath` | — | File download | Explorer |
| `POST` | `/collections` | `{username, collection_name, visibility, tags}` | `{id, username, collection_name}` | LLMAssistant |
| `POST` | `/collections/:username/:coll/entries` | `{path, hash}` | — | Explorer, LLMAssistant |
| `DELETE` | `/collections/:username/:coll/entries/:path` | — | — | Explorer, LLMAssistant |
| `POST` | `/collections/:username/:coll/commit` | `{commit_message}` | `{message, version_number, snapshot_hash}` | Explorer, LLMAssistant |
| `GET` | `/collections/:username/:coll/log` | — | `{data: [{id, version_number, commit_message, created_at}]}` | VersionLog, LLMAssistant |
| `POST` | `/collections/:username/:coll/rollback/:vid` | — | — | VersionLog |

### 4.4 Actions (Fork / Merge)

| Method | Path | Body | Response | Used By |
|--------|------|------|----------|---------|
| `POST` | `/actions/fork` | `{username, source_username, collection_name, source_coll_name}` | — | LLMAssistant |
| `POST` | `/actions/merge` | `{username, source_username, collection_name, source_coll_name, strategy}` | `{total_entries, entries}` | Explorer |
| `POST` | `/actions/pull` | `{username, collection_name}` | — | (unused in frontend) |

### 4.5 P2P

| Method | Path | Body | Response | Used By |
|--------|------|------|----------|---------|
| `GET` | `/p2p/node` | — | `{peer_id, addresses}` | Settings, LLMAssistant |
| `GET` | `/p2p/status` | — | `{peer_id, relay_mode, hole_punch, ws_connections, addresses}` | LLMAssistant |
| `GET` | `/p2p/peers` | — | `[{peer_id, ...}]` | P2PStatus |
| `GET` | `/p2p/discovered` | — | `[{peer_id, addrs}]` | P2PStatus |
| `GET` | `/p2p/ping/:peerId` | — | `{latency or rtt}` | P2PStatus |
| `POST` | `/p2p/connect` | `{peer_id, addrs}` | — | P2PStatus |
| `POST` | `/p2p/announce` | `{hash}` | — | (unused in frontend) |
| `POST` | `/p2p/fetch` | `{peer_id, hash}` | `{hash or collection_hash}` | P2PStatus |
| `POST` | `/p2p/sync` | `{peer_id, collection_name}` | — | P2PStatus |
| `POST` | `/p2p/push` | `{peer_id, collection_name}` | — | P2PStatus |
| `POST` | `/p2p/request-file` | `{hash}` | — | P2PStatus |
| `GET` | `/p2p/ws/info` | — | `{connections: [{peer_id, remote_addr}]}` | P2PStatus |

### 4.6 Other

| Method | Path | Body | Response | Used By |
|--------|------|------|----------|---------|
| `GET` | `/ping` | — | — | Settings, LLMAssistant |
| `GET` | `/tasks` | — | `[{id, ...}]` | LLMAssistant |
| `GET` | `/tasks/:id` | — | Task status | (unused in frontend) |
| `POST` | `/local/save` | `{collection_hash, local_path, include, exclude}` | — | Explorer |
| `GET` | `/local/status/:hash` | — | `{saved_files, total_files, missing_files, collection_hash}` | Explorer |

---

## 5. State Management

### 5.1 AppContext

**File:** `src/App.jsx` (line 13)

```jsx
export const AppContext = createContext();

// Provided values:
{
  username: string,          // From localStorage('peerdrive_username')
  setUsername: (val) => void, // Updates state + localStorage
  nodeInfo: object | null,   // P2P node info (set by nowhere in current App.jsx)
  setNodeInfo: (info) => void
}
```

- `username` is initialized from `localStorage.getItem('peerdrive_username')`
- `setUsername` writes to both React state and localStorage
- `nodeInfo` / `setNodeInfo` are exposed in context but never set by App — possibly set by child components or legacy code

### 5.2 PageContext

**File:** `src/App.jsx` (line 14)

```jsx
export const PageContext = createContext({
  pageContext: null,
  setPageContext: () => {}
});

// Each page sets pageContext on mount via useEffect:
// Plaza:        not set
// FileManager:  { type: 'fileManager', fileCount, sortBy, category }
// Explorer:     { type: 'explorer', username, collectionName, entryCount, entries }
// AnonCreator:  { type: 'anonCreator', fileCount, entryCount, friendlyName, openHash }
// AnonExplorer: not set
// Settings:     not set
```

- Used by `LLMAssistant.buildContext()` to inject current page state into the LLM system prompt
- `PageContext` default value wraps `setPageContext` in an object — consuming components must use `|| {}` fallback when destructuring (e.g., `const { pageContext } = useContext(PageContext) || {}`)

### 5.3 localStorage Keys

| Key | Type | Default | Set By |
|-----|------|---------|--------|
| `peerdrive_api_base` | string | `https://wsl-3000.moonchan.xyz` | Settings |
| `peerdrive_username` | string | `''` | App.setUsername |
| `peerdrive_auth_key` | string | `''` | Settings |
| `peerdrive_llm_endpoint` | string | `https://siliconflow.moonchan.xyz` | Settings |
| `peerdrive_llm_model` | string | `Qwen/Qwen3-8B` | Settings |
| `peerdrive_llm_apikey` | string | `''` | Settings |
| `peerdrive_llm_body_template` | JSON string | `{model, messages:[], stream:true, max_tokens:4096, temperature:0.7}` | Settings |
| `peerdrive_data_consent` | `'true'` or `'false'` | `false` | Settings |
| `peerdrive_consent` | JSON string | — | `uploadConsent()` |

---

## 6. Known Issues & Gotchas

### 6.1 React Router v7

1. **`useNavigate()` has no `.replace()` method**
   - WRONG: `nav.replace('/path')`
   - CORRECT: `nav('/path', { replace: true })`
   - Used in AnonCreator line 77-88 to clear navigation state

2. **`useLocation().state` can be `null`**
   - Defaulting with `|| {}` covers `undefined` but NOT `null`
   - Safe pattern: `useLocation().state || {}`
   - Used in AnonCreator line 49

### 6.2 FileTree Crashes

1. **`node.files.length` on non-directory nodes** (`FileTree.jsx:113`)
   - In `buildTree()`, if a node has no `_files` and no `_children`, `toArray()` might produce a node where `files` is `undefined`
   - The render loop at line 113 accesses `node.files.length` without null check
   - **Workaround**: Ensure `buildTree` always sets `files: []` on directory nodes

2. **Empty entries + drag**: The empty state handler at line 162 parses `text/plain` with `JSON.parse` — will throw if drag data is plain filename text

### 6.3 Checkbox Styling

- On dark backgrounds (`bg-gray-800`, `bg-gray-900`), standard checkboxes are invisible
- Must use `className="accent-cyan-500"` for visibility (used in FileManager list + tree views)
- Cascade checkboxes in FileManager tree use this explicitly

### 6.4 Drag-and-Drop MIME Types

- **Cross-browser compatibility requires both custom and standard MIME types:**
  - `application/peerdrive-file` — custom, for structured file data
  - `application/peerdrive-entry` — custom, for tree entries
  - `application/peerdrive-path` — custom, for directory paths
  - `text/plain` — fallback for browsers that strip custom MIME types
- Receivers should try custom types first, then fall back to `text/plain`
- Senders should always set both custom type AND `text/plain`

### 6.5 LLM Endpoint

- **The LLM proxy MUST be `siliconflow.moonchan.xyz`, NOT `wsl-3000.moonchan.xyz`**
- `wsl-3000` is the Peerdrive API server; it does NOT serve LLM endpoints
- The default API base (`wsl-3000`) is for file/collection operations only
- LLM requests go to `{llm_endpoint}/v1/chat/completions`

### 6.6 Cloudflare Pages Deployment

- **`public/_redirects` must exist** with content: `/* /index.html 200`
- **`build.sourcemap` must be `true`** in `vite.config.ts`
- Build is executed by Cloudflare (not locally); all config must be in git
- `dist/` is gitignored — never manually commit build artifacts
- Push to `frontend` branch triggers auto-deploy

### 6.7 Known Traps (from AGENTS.md)

1. **TDZ (Temporal Dead Zone)**: Variables in `useEffect` dependency arrays must be declared before the `useEffect` call
2. **useState closures**: In multi-turn async loops (like LLMAssistant's tool calling), callback closures may reference stale state. Use local accumulator variables (`accMessages`) instead
3. **Component organization**: SearchPanel is defined inline in Navbar.jsx — not separately testable

---

## 7. Testing

### 7.1 Test Files

| File | Contents |
|------|----------|
| `tests/FileTree.test.jsx` (28 lines) | 4 tests: empty state, flat entries, nested paths, new folder button |
| `tests/smoke.test.jsx` (35 lines) | 4 smoke tests: Sha256Manager, CollectionBuilder, P2PStatus, App |
| `tests/setup.js` | `afterEach(cleanup)` from @testing-library/react |

### 7.2 How to Run Tests

```bash
# In /mnt/d/WorkPlace/peerdrive/react:
npm test
```

This runs `vitest run` (single pass, non-watch mode).

### 7.3 Test Configuration

**File:** `vitest.config.ts`
```ts
{
  plugins: [react()],
  test: {
    environment: 'happy-dom',
    setupFiles: ['./tests/setup.js'],
  }
}
```

### 7.4 Current Test Status

- **FileTree.test.jsx**: 4 tests (empty state render, flat entries render, nested path folder structure, new folder button presence) — all are pure render tests with no interaction testing
- **smoke.test.jsx**: 4 tests (Sha256Manager, CollectionBuilder, P2PStatus, App all render without crashing)
- **Test coverage**: Minimal — only render-crash checks. No interaction tests (drag-and-drop, async API calls, routing, LLM streaming, modal interactions, checkbox selection)
- **Missing tests**: Navbar, AnonCreator, AnonExplorer, Explorer, FileManager (full), Settings, LLMAssistant, VersionLog
- **API calls**: Not mocked in tests — components that call `api.*` on mount will fail unless backend is running; the smoke test for App may throw due to unhandled promise rejections from Plaza's `useEffect`

### 7.5 Testing Gotchas

- Components that call `useContext(AppContext)` or `useContext(PageContext)` need providers wrapped around them in tests
- Components using `useNavigate()`, `useLocation()`, `useParams()` need `MemoryRouter` wrapper
- The `api.js` module uses `localStorage` — tests must mock or `happy-dom` provides it
- FileTree's `Select all` elements require `@testing-library/user-event` for interaction tests (not yet configured)

---

## 8. Additional Components

These components exist in `src/components/` but are not directly rendered as routes in App.jsx:

| Component | File | Lines | Purpose | Note |
|-----------|------|-------|---------|------|
| `VersionLog` | `VersionLog.jsx` | 53 | Rendered inside Explorer page, shows commit history + rollback | Used by Explorer |
| `CollectionBuilder` | `CollectionBuilder.jsx` | 551 | Full anon collection build + browse + fork UI | Used by AnonCreator |
| `AnonCollectionManager` | `AnonCollectionManager.jsx` | 225 | Browse + create + fork anon collections | Used by AnonCreator |
| `P2PStatus` | `P2PStatus.jsx` | 474 | Full P2P network status dashboard with WebSocket | Used by P2P pages |
| `PeerDetailPanel` | `PeerDetailPanel.jsx` | — | Peer detail table with protocol version, transports, latency | Used by P2PDashboard |
| `Sha256Manager` | `Sha256Manager.jsx` | 79 | SHA256 hash lookup + metadata display | Used by FileManager |
| `PathRegistrar` | `PathRegistrar.jsx` | 107 | Register local file/folder paths | Used by FileManager |
| `MobileNav` | `MobileNav.jsx` | — | Bottom navigation bar for mobile (md+ hidden) | App.jsx global |
