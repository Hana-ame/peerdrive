# Pages / Routes / Navigation (front/src/pages + App.jsx + Navbar)

> Layer membership: AOP ⑧ Frontend aspect (doc/LAYERS.md §1). React 19 + react-router-dom 7
> single-page app: App.jsx defines all routes and global state; pages/ contains
> business pages; components/Navbar.jsx and LLMAssistant.jsx are cross-page global
> components. All data communicates with the local node via api.js (→ ws.js).

## Responsibilities

- **App.jsx**: route table, `AppContext` (username/nodeInfo), `PageContext`
  (page context for the LLM assistant to sense the current page), `AppErrorBoundary`
  global error boundary.
- **pages/**: 12 page components + 2 page-level component trees (AnonCreator,
  AnonExplorer), each aggregating business UI and api.js calls.
- **components/**: Navbar (navigation + Ctrl+K global search), LLMAssistant (AI
  assistant), FileTree/VersionLog/CollectionCard/CommentSection/SettingsSection
  (cross-page reusable pieces).

## Key mechanisms

### Route table (App.jsx:96-118, all real routes)

| Route | Component | Responsibility |
|---|---|---|
| `/` | Plaza | Collection plaza |
| `/:username/:collName` | HashRedirect → Explorer | User collection browser (64hex params auto-redirect) |
| `/create` | AnonCreator | Anonymous collection creator |
| `/c/:hash` | AnonExplorer | Anonymous collection viewer (unified route) |
| `/anon/collections/:hash` | AnonExplorer | Anonymous collection viewer (legacy path) |
| `/anon` | AnonExplorer | Empty-hash viewer (shows EmptyState) |
| `/p2p` | P2PPanel | P2P dual-stack control |
| `/ipfs` | IPFSPanel | IPFS overview |
| `/bt` | BTController | BT download controller |
| `/bt/controller` | Navigate → `/bt` | Old path redirect |
| `/bt/status` | BTPanel | BT DHT status |
| `/bt/dht` | DHTExplorer | BT DHT query |
| `/p2p/dashboard` | P2PDashboard | P2P network dashboard |
| `/p2p/topology` | P2PTopology | Network topology SVG |
| `/ipfs/dht` | DHTExplorer | IPFS DHT query (shared component) |
| `/settings` | Settings | Settings page (7 sections) |
| `*` | Navigate → `/` | Unknown path fallback, avoids blank page |

### HashRedirect (App.jsx:25-36)

If any segment of the `/u/coll` two-segment route matches a 64-hex string
(`HASH_RE = /^[a-f0-9]{64}$/i`), a declarative
`<Navigate to={/c/<hash>} replace />` converts to the unified collection route.

**Pitfall**: the old way called `navigate()` during render (side effect); react-router
warns and StrictMode double-fires. Declarative `<Navigate>` only returns an element
during render, no side effects.

### Global state (App.jsx:20-21, 89-90)

- `AppContext`: `username` (persisted in localStorage `peerdrive_username`), `nodeInfo`.
- `PageContext`: `pageContext`/`setPageContext` —— each page reports semantic data
  about the current page on mount (Explorer reports entries summary, AnonCreator
  reports fileCount/entryCount, AnonExplorer reports collection name); LLMAssistant
  consumes it to generate context-aware answers.
- `AppErrorBoundary` (App.jsx:39-74): a single page/component error doesn't blank the
  whole tree; shows a "Retry" button (`getDerivedStateFromError` + `handleRetry` clears
  error state).

### Page inventory (filename → one-line responsibility)

| File | Responsibility |
|---|---|
| `Plaza.jsx` | Collection plaza: local anonymous collections + P2P public collections dual tabs (grid/list), hash search direct jump, fork source passed to `/create`; shows DUMMY sample cards when no backend |
| `Explorer.jsx` | User collection browser: entry folder navigation, upload (uploadFile + addEntry), commit version, merge (modal with anonymous-source client merge), save snapshot to local (/local/save), right-side VersionLog |
| `AnonCreator/index.jsx` | Anonymous collection creator: three columns (left: file source filter/collection browser/system directory browser; middle: preview; right: entry editor), drag & drop/batch save/URL registration, mobile tab switching |
| `AnonExplorer/index.jsx` | Anonymous collection viewer: hash search (type-to-jump), folder navigation, single/multi file views, preview (image/text/PDF/generic), nested collection links, comments, save copy |
| `Settings.jsx` | Settings 7 sections: node connection (multi-backend) / auth / storage management / IPFS compat / LLM config / WebDAV (mount info) / About (version and data) |
| `P2PPanel.jsx` | P2P dual-stack control: IPFS DHT + BT DHT dual-network announce/lookup/file sharing lifecycle |
| `P2PDashboard.jsx` | P2P network dashboard: node/peer connections/file announce and lookup status, RTT coloring |
| `P2PTopology.jsx` | Network topology: SVG display of connected peer nodes and status |
| `BTPanel.jsx` | BT DHT status monitoring: Infohash announce and lookup |
| `BTController.jsx` | BT download controller: qBittorrent-like download management (magnet/torrent upload/pause/resume/seed/remove) |
| `DHTExplorer.jsx` | DHT query tool: shared by `/bt/dht` and `/ipfs/dht` |
| `IPFSPanel.jsx` | IPFS panel: CID pin, pin list, gateway status, libp2p monitoring |

### Common page patterns (shared by the three collection pages)

1. **Folder-style navigation** (three independent implementations in Explorer /
   AnonExplorer / AnonCreator CollBrowser): `navPath` state + entry path prefix splits
   current-level dirs/files; `navIn(dir)`/`navBack()` enter/exit directories. Entry
   path is the collection-relative path; folders are implicit (no independent dir
   objects).
2. **PageContext reporting**: on mount/data change call `setPageContext({type, ...})`
   to give LLMAssistant the current page semantics (Explorer reports
   `type:'explorer'` + entries summary, AnonCreator reports `type:'anonCreator'` +
   counts, see App.jsx:21).
3. **Toast pattern**: AnonCreator/AnonExplorer each maintain `toastMsg`/`toastErr` +
   `toastTimerRef` (pitfall 10), no global notification system.
4. **Hash extraction**: Plaza/AnonExplorer/SearchBar share the `extractHash` regex
   `/\b([a-f0-9]{64})\b/i`, paste/type is recognized and jumps.

### Page-level subcomponent trees

**AnonCreator/** (17 files): `LeftPanel` (filter + list, aggregating CollectionRow/
CollFileRow/CollBrowser/SystemBrowse/SearchHistory), `MiddlePanel` (preview),
`RightPanel` (editor, aggregating EditorPanel/EditorToolbar/FileTree/NamePrompt),
`CollBrowser` (collection browser, aggregating CollBrowserNav), `constants.js`
(SOURCE_TABS/sort options), `utils.js` (fileIcon/fmtSize/collDisplayName/search
history).

**AnonExplorer/** (16 files): `SearchBar` (hash input), `CollectionHeader` (name/
tags/visibility/save copy), `BreadcrumbNav`, `FileList`+`FileRow`, `SingleFilePreview`
(single file direct preview, aggregating ImagePreview/TextPreview/PdfPreview/
GenericFilePreview/NestedCollectionLink), `Toast`, `EmptyState`, `utils.js`
(extractHash).

Preview dispatch logic (SingleFilePreview.jsx:41-46): MIME prefix + extension double
check —— `image/*` → ImagePreview, `text/*` or common code extensions → TextPreview,
`application/pdf` → PdfPreview, others → GenericFilePreview; entry hash hits local
collection set (`allCollHashes`) → NestedCollectionLink (nested collection jump).
Preview data always via `api.downloadAnonFile` (admin-bin binary response) → Blob
objectURL, **no HTTP URLs**.

### Mobile adaptation

- AnonCreator: three columns collapse to a single column + top tab switching below the
  `md` breakpoint (files/preview/editor, `mobilePanel` state, index.jsx:461-478);
  after selecting a file auto-switches to preview tab.
- Navbar: desktop links collapse into a hamburger drawer (Portal to body,
  `mobileMenuOpen` state, Navbar.jsx:355-383); NavDropdown degrades to click toggling
  on touch.

### Global components

- **Navbar.jsx** (388 lines): top navigation (desktop links + mobile drawer Portal),
  `NavDropdown` dropdown (Portal to body to avoid overflow clipping, hover/click dual
  interaction), `SearchPanel` global search (Ctrl+K or `/` shortcut; 200ms debounce;
  ↑↓ select + Enter to open; local/public collections split).
- **LLMAssistant.jsx** (654 lines): bottom-right AI assistant panel, 17 function tools
  (navigate_to / get_node_info / list_collections / list_public_collections /
  search_collections / create_collection / get_collection_info /
  add_file_to_collection / commit_collection / fork_collection / get_version_log /
  register_local_file / register_folder / get_tasks / get_p2p_status /
  create_anon_collection / verify_file), direct connection to LLM endpoint
  (`POST {endpoint}/v1/chat/completions`, api.js's LLM config), tool execution calls
  api.js.
- **FileTree.jsx**: entry directory tree (drag/rename/move/create folder), exports
  `buildFlatTree`.
- **VersionLog.jsx**: version history + rollback.
- **CollectionCard.jsx**: collection card (grid/list two modes).
- **CommentSection.jsx**: collection comments (direct regserver).
- **SettingsSection.jsx**: settings section card (title/description/subcontent/save).

## Relationships with other modules

```
main.jsx (mounts #root)
  → App.jsx (routes + Context + ErrorBoundary)
      ├─ Navbar / LLMAssistant (global, resident)
      ├─ pages/* (route components, import * as api)
      │     └─ components/* (reusable pieces)
      └─ api.js → ws.js → /ws/peer (only backend channel)
```

- **Up (entry)**: `main.jsx` only does `createRoot(...).render(<App />)`.
- **Down (api.js)**: all page data goes through api.js; preview/download get Blobs via
  `downloadAnonFile`/`downloadUserFile`/`getBlobUrl` (WS channel, not HTTP URL).
- **Sideways (inter-component)**: `PageContext` is the only data channel from pages →
  LLMAssistant; navigation is all through react-router `useNavigate`/`Link` (in-SPA,
  no full page refresh).
- **Peer (backend)**: pages only perceive api.js named functions, unaware of WS/HTTP
  transport (LAYERS.md §5: frontend forbidden from directly fetching local HTTP
  endpoints).

## Caveats and design decisions

1. **HashRedirect declarative Navigate**: side-effect navigate during render double-fires
   under StrictMode (App.jsx:27-28).
2. **AnonExplorer request sequence guard (fetchSeqRef)**: with type-to-jump/paste/card
   clicks rapidly switching hashes, old requests' slow responses must not overwrite the
   current collection (index.jsx:29-32, 49-53).
3. **Empty collections are not auto-deleted**: `fetchCollection` explicitly comments not
   to `api.deleteFile` empty collections —— read interface with write side effects;
   without a registration server it really deletes storage files, with one it yields a
   401 then mismatched messages (AnonExplorer/index.jsx:53-55).
4. **navigate(-1) deep-link pitfall**: deep links opened in a new tab have no history,
   when `history.length≈1` fallback goes to the home page instead of leaving the SPA
   (AnonExplorer SearchBar fallback logic).
5. **Plaza "Antenna" tab removed**: BEP51 can only sample 20-byte infohashes from
   torrent DHT, never getting the 64-bit hashes peerdrive collections announce; the
   click-to-open closed loop is unreachable (Plaza.jsx:86-87, git log has the discovery
   context).
6. **AnonCreator forkFrom has no entries**: what Plaza cards pass is AnonCollectionSummary
   (only hash/friendly_name/entry_count); direct `setEntries(c.entries||[])` is always
   an empty collection —— must pull the full data by hash (index.jsx:73-83).
7. **addEntry removed 500ms lastClick debounce**: programmatic batch adds
   (handleSysAddFolder/drag) get silently dropped by the time gate (added N but only
   1-2 actually enter) —— changed to deduplicate by path+hash inside the reducer
   (AnonCreator/index.jsx:204-206).
8. **URL provider recognition**: collection entries may only have URL provider (no
   sha256); treating them all as sha256 generates illegal 64-bit hashes rejected by the
   backend —— recognize the `^https?://` form and store as a url provider
   (index.jsx:207-211).
9. **Directory rename/move must cascade to subentries**: only changing entries with an
   exactly-equal path orphans `dir/a.txt` floating to the root; move is "delete from
   source" semantics not copy; target cannot be itself/a subdirectory; put the
   sanity-check outside the `setEntries` callback (keep state updater pure, StrictMode
   double-fires side effects) (index.jsx:227-263).
10. **Toast timer must reuse a ref**: creating a new setTimeout each time causes the
    previous toast's timer to clear the message before 3s (rapid consecutive operations
    flash by) (index.jsx:172-179).
11. **Anonymous collection merge is client-side**: backend `/actions/merge` only accepts
    user collection pairs, no anonymous source endpoint —— `mergeAnonIntoLocal` compares
    hashes by path in the browser to implement ours/theirs/manual three strategies;
    manual's 409 conflict list is displayed in a modal and supports strategy-switch
    retry (Explorer.jsx:111-165).
12. **SearchPanel activeIdx negative**: when total=0 the old implementation
    `Math.min(i+1, total-1)` → -1, highlighting is meaningless —— empty results clamp
    to 0 (Navbar.jsx:146-148).
13. **Route fallback `*`**: unknown paths Navigate back to `/`, avoiding blank pages
    (App.jsx:117).

## Tests

No page-level E2E framework (vitest + happy-dom + testing-library); coverage:

| File | Coverage |
|---|---|
| `tests/smoke.test.jsx` | App renders without crashing (`screen.getByText('Peerdrive')`) |
| `tests/components.test.jsx` | Page/component basic render (App/Plaza/AnonCreator/AnonExplorer/Settings/VersionLog/Navbar/EditorToolbar/FileTree) |
| `tests/FileTree.test.jsx` | FileTree render + `buildFlatTree` flattening logic |

- All component tests go through `tests/setup.js` global `vi.mock('../src/api.js')`
  (see api-layer.md Tests section), pages produce no real network requests.
- Manual verification: `npm run dev` starts the frontend + local node, walk the full
  flow (create collection → upload → browse → download → P2P/BT/IPFS panels →
  settings). Playwright smoke scripts are in `front/tests/`
  (playwright-smoke.mjs, pw-settings-mobile.mjs), connecting to host CDP browser use
  (see global AGENTS.md playwright-test skill).

## File inventory

- `front/src/main.jsx` (7 lines) —— entry
- `front/src/App.jsx` (127 lines) —— routes/Context/ErrorBoundary
- `front/src/pages/` (10 top-level pages + 2 page component trees, 41 files total) ——
  see the "Page inventory" table
- ⚠️ `front/src/components/` **已不存在**（v3 重构后组件拆进各页面，前端只剩 `pages/` `lib/` `api.js` `ws.js`）。原先的 7 个文件（Navbar/LLMAssistant/FileTree/VersionLog/
  CollectionCard/CommentSection/SettingsSection)
- `front/tests/` (smoke.test.jsx / components.test.jsx / FileTree.test.jsx +
  playwright smoke scripts)
- Related docs: `doc/layers/L8-frontend/ws-client.md` (data channel),
  `api-layer.md` (API surface)
