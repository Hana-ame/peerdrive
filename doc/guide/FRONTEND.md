# Peerdrive Frontend Documentation

> React 19 SPA — Vite 8 · React Router 7 · Tailwind CSS 3 · Vitest
> Updated: 2026-04-30

---

## Table of Contents

1. [Tech Stack](#1-tech-stack)
2. [Project Structure](#2-project-structure)
3. [Routing & Pages](#3-routing--pages)
4. [Component Tree](#4-component-tree)
5. [State Management](#5-state-management)
6. [API Communication Layer](#6-api-communication-layer)
7. [Style System](#7-style-system)
8. [PWA Support](#8-pwa-support)
9. [Core Workflows](#9-core-workflows)
10. [Local Development](#local-development)
11. [Appendix: Code Map](#appendix-code-map)

---

## 1. Tech Stack

| Category | Technology | Version |
|------|------|------|
| Build tool | Vite | ^8.0 |
| UI framework | React | ^19.2 |
| Routing | React Router DOM | ^7.14 |
| CSS | Tailwind CSS | ^3.4 |
| Testing framework | Vitest | ^4.1 |
| Test environment | Happy DOM | ^20.9 |
| Component testing | Testing Library (React) | ^16.3 |

- **No TypeScript** — Pure JavaScript + JSX
- **No global state library** — Uses React Context for global state
- **ES Module** — `"type": "module"`

---

## 2. Project Structure

```
front/
├── index.html              # HTML entry (includes PWA meta tags)
├── package.json
├── vite.config.ts          # Vite build configuration
├── vitest.config.ts        # Test configuration
├── tailwind.config.js      # Tailwind configuration
├── postcss.config.js
├── public/
│   ├── favicon.svg
│   ├── manifest.json       # PWA manifest
│   ├── service-worker.js   # Service Worker
│   └── icons/
│       └── icon-192.png
├── src/
│   ├── main.jsx            # React entry: createRoot + render
│   ├── App.jsx             # Root component: Context Provider + route definitions
│   ├── index.css           # Tailwind directives + global styles
│   ├── api.js              # HTTP client + localStorage configuration
│   ├── components/         # Reusable components (15)
│   │   ├── Navbar.jsx          # Top navigation bar + global search (Ctrl+K)
│   │   ├── MobileNav.jsx       # Mobile bottom navigation bar
│   │   ├── LLMAssistant.jsx    # LLM conversation assistant floating window
│   │   ├── FileTree.jsx        # File tree display component
│   │   ├── CollectionCard.jsx  # Collection card
│   │   ├── CollectionBuilder.jsx # Collection builder
│   │   ├── Sha256Manager.jsx   # SHA256 addressing management
│   │   ├── VersionLog.jsx      # Version history
│   │   ├── CommentSection.jsx  # Comment section
│   │   ├── P2PStatus.jsx       # P2P network status
│   │   ├── WebRTCPeer.jsx      # WebRTC peer connection
│   │   ├── WebRTCTransfer.jsx  # WebRTC file transfer
│   │   ├── ServiceStatus.jsx   # Service health dashboard
│   │   ├── ActiveConnPanel.jsx # Active connections panel
│   │   ├── PeerDetailPanel.jsx # Peer node details
│   │   ├── SettingsSection.jsx # Settings section wrapper
│   │   ├── VisibilityPicker.jsx # Visibility selector
│   │   ├── UserGroupPicker.jsx # User/group selector
│   │   ├── PathRegistrar.jsx   # Path registrar
│   │   └── AnonCollectionManager.jsx # Anonymous collection manager
│   ├── pages/              # Page-level components (9 modules)
│   │   ├── Plaza.jsx           # Collection plaza (home page)
│   │   ├── Explorer.jsx        # Registered user collection browser
│   │   ├── FileManager.jsx     # File management
│   │   ├── Settings.jsx        # Settings page
│   │   ├── P2PPanel.jsx        # P2P overview panel
│   │   ├── P2PDashboard.jsx    # P2P dashboard
│   │   ├── P2PTopology.jsx     # P2P topology graph
│   │   ├── DHTExplorer.jsx     # DHT routing table browser
│   │   ├── IPFSPanel.jsx       # IPFS panel
│   │   ├── BTPanel.jsx         # BT overview panel
│   │   ├── BTController.jsx    # BT download controller
│   │   ├── AnonCreator/        # Anonymous collection creator (multiple subcomponents)
│   │   │   ├── index.jsx
│   │   │   ├── LeftPanel.jsx       # Left file source panel
│   │   │   ├── MiddlePanel.jsx     # Middle collection content panel
│   │   │   ├── RightPanel.jsx      # Right edit/preview panel
│   │   │   ├── EditorPanel.jsx     # Text editor
│   │   │   ├── EditorToolbar.jsx   # Editor toolbar
│   │   │   ├── CollBrowser.jsx     # Collection browser
│   │   │   ├── CollBrowserNav.jsx  # Collection browsing navigation
│   │   │   ├── CollectionHeader.jsx
│   │   │   ├── CollectionRow.jsx
│   │   │   ├── CollFileRow.jsx
│   │   │   ├── FileSourceRow.jsx
│   │   │   ├── SourceFilters.jsx   # File source filters
│   │   │   ├── SourceTabs.jsx      # File source tab switching
│   │   │   ├── SystemBrowse.jsx    # System file browser
│   │   │   ├── RegisteredView.jsx  # Registered files view
│   │   │   ├── SearchHistory.jsx   # Search history
│   │   │   ├── TimelineView.jsx
│   │   │   ├── SplitHandle.jsx
│   │   │   ├── NamePrompt.jsx
│   │   │   └── Toast.jsx
│   │   └── AnonExplorer/       # Anonymous collection explorer
│   │       ├── index.jsx
│   │       ├── SearchBar.jsx
│   │       ├── BreadcrumbNav.jsx
│   │       ├── CollectionHeader.jsx
│   │       ├── EmptyState.jsx
│   │       ├── FileList.jsx
│   │       ├── FileRow.jsx
│   │       ├── ImagePreview.jsx
│   │       ├── PdfPreview.jsx
│   │       ├── TextPreview.jsx
│   │       ├── GenericFilePreview.jsx
│   │       ├── SingleFilePreview.jsx
│   │       ├── NestedCollectionLink.jsx
│   │       └── Toast.jsx
│   └── storage/            # Local storage layer
│       ├── localDB.js      # IndexedDB wrapper
│       └── syncManager.js  # Local sync manager
└── tests/                  # Test files
    ├── setup.js
    ├── smoke.test.jsx
    ├── FileTree.test.jsx
    ├── components.test.jsx
    └── playwright-smoke.mjs
```

---

## 3. Routing & Pages

| Route | Page Component | Description |
|-------|---------------|------|
| `/` | `Plaza` | Collection plaza - browse public collections |
| `/:user/:collection` | `Explorer` | User collection browser |
| `/anon/create` | `AnonCreator` | Create anonymous collection |
| `/anon/:hash` | `AnonExplorer` | Browse anonymous collection |
| `/files` | `FileManager` | File management |
| `/settings` | `Settings` | Settings page |
| `/p2p` | `P2PPanel` | P2P overview |
| `/p2p/dashboard` | `P2PDashboard` | P2P dashboard |
| `/p2p/topology` | `P2PTopology` | P2P topology |
| `/ipfs` | `IPFSPanel` | IPFS panel |
| `/bt` | `BTPanel` | BT overview |
| `/bt/controller` | `BTController` | BT download controller |
| `/dht` | `DHTExplorer` | DHT browser |

---

## 4. Component Tree

```
<App>
├── <ServiceContext.Provider>     # Service status context
├── <AuthContext.Provider>        # Authentication context
├── <FileContext.Provider>        # File state context
├── <Navbar />                    # Top navigation + search
├── <MobileNav />                 # Mobile navigation
├── <LLMAssistant />              # LLM assistant (floating)
├── <ServiceStatus />             # Backend status indicator
├── <Routes>
│   ├── <Plaza />                 # Home page
│   ├── <Explorer />              # User collection
│   ├── <AnonCreator />           # Anonymous collection creation
│   ├── <AnonExplorer />          # Anonymous collection browsing
│   ├── <FileManager />           # File management
│   ├── <Settings />              # Settings
│   ├── <P2PPanel />              # P2P panel
│   ├── <P2PDashboard />          # P2P dashboard
│   ├── <P2PTopology />           # P2P topology
│   ├── <IPFSPanel />             # IPFS panel
│   ├── <BTPanel />               # BT panel
│   ├── <BTController />          # BT controller
│   └── <DHTExplorer />           # DHT explorer
└── <FileTree />                  # Global file tree (context-based)
```

---

## 5. State Management

React Context-based, three major contexts:

### ServiceContext
- `isOnline`: Backend service online status
- `checkService()`: Ping backend health
- `updateServiceStatus()`: Update status

### AuthContext
- `user`: Current user info
- `token`: JWT token
- `isAuthenticated`: Authentication state
- `login()`, `logout()`, `register()`

### FileContext
- `files`: File list
- `currentPath`: Current directory
- `uploadFile()`, `deleteFile()`, `moveFile()`
- `collections`: Collection list
- `currentCollection`: Current collection

---

## 6. API Communication Layer

### api.js Structure

```javascript
// Base URL and token management
const API_BASE = 'http://localhost:3000';  // Hardcoded, change for production
const TOKEN_KEY = 'peerdrive_token';

// HTTP helper functions
async function apiFetch(path, options) { ... }
async function get(path) { ... }
async function post(path, body) { ... }
async function put(path, body) { ... }
async function del(path) { ... }

// Token management
function getToken() { ... }
function setToken(token) { ... }
function clearToken() { ... }

// API modules (grouped by function)
const authApi = { register, login, whoami, logout };
const fileApi = { upload, list, get, delete, move, rename };
const collectionApi = { create, list, get, commit, merge, fork };
const p2pApi = { status, connect, download, announce, find };
const btApi = { status, announce, find, bep44Put, bep44Get };
const ipfsApi = { announce, find, cid };
const relayApi = { status, register, unregister };
const commentApi = { list, add, delete };
const webdavApi = { list, get, upload };
```

### localStorage Keys

| Key | Purpose |
|------|------|
| `peerdrive_token` | JWT authentication token |
| `peerdrive_username` | Current username |
| `peerdrive_llm_endpoint` | LLM API endpoint |
| `peerdrive_llm_model` | LLM model name |
| `peerdrive_llm_apikey` | LLM API key |
| `peerdrive_llm_body_template` | LLM request body template |

---

## 7. Style System

- **Tailwind CSS 3.4** — Utility-first CSS framework
- **Custom theme** defined in `tailwind.config.js`:
  - Primary colors: blue palette
  - Dark mode support
  - Custom breakpoints
- **index.css** — Tailwind directives + custom global styles
- **No CSS Modules** — All styles via Tailwind classes

### Key Style Classes

```
Primary button:   bg-blue-600 hover:bg-blue-700 text-white
Card:             bg-white dark:bg-gray-800 rounded-lg shadow
Navigation:       bg-gray-900 text-white
Input:            bg-gray-100 dark:bg-gray-700 border border-gray-300
Error state:      text-red-600 bg-red-50
Success:          text-green-600 bg-green-50
```

---

## 8. PWA Support

- **manifest.json** — PWA manifest with icons, theme color
- **service-worker.js** — Offline caching, asset caching
- **Icons** — 192x192 and 512x512 PNG icons
- **Offline support** — Cache-first strategy for static assets

---

## 9. Core Workflows

### User Registration Flow
1. User enters username/password on login page
2. `authApi.register()` → `POST /auth/register`
3. Store returned token in localStorage
4. Navigate to Plaza

### File Upload Flow
1. User selects file in Explorer
2. `fileApi.upload()` → `POST /files/upload` (multipart/form-data)
3. Backend computes SHA256, returns hash
4. File appears in current directory listing

### Collection Commit Flow
1. User modifies collection files in Explorer
2. Clicks "Commit" button
3. `collectionApi.commit()` → `POST /collections/:user/:name/commit`
4. New version created, VersionLog updates

### Anonymous Collection Browse Flow
1. User enters `/anon/:hash`
2. `fileApi.getAnonymous()` → `GET /anon/collections/:hash`
3. File list renders with preview capability
4. Click file → `fileApi.download()` streams file

---

## 10. Local Development

### Setup

```bash
cd front
npm install
npm run dev
```

### Configuration

- Dev server runs on `http://localhost:5173`
- Vite proxy configured in `vite.config.ts` for API calls
- Change `API_BASE` in `src/api.js` for backend address

### Build

```bash
npm run build       # Production build
npm run preview     # Preview production build
npm run test        # Run tests
npm run test:watch  # Watch mode tests
```

---

## 11. Appendix: Code Map

### Key File Map

| File | Purpose |
|------|------|
| `src/main.jsx` | Application entry point |
| `src/App.jsx` | Root layout and routing |
| `src/api.js` | API client and configuration |
| `src/index.css` | Global styles |

### Component Map

| Component | Purpose |
|------|------|
| `Navbar.jsx` | Top navigation bar with search |
| `MobileNav.jsx` | Mobile bottom navigation |
| `LLMAssistant.jsx` | AI assistant floating panel |
| `FileTree.jsx` | File tree browser |
| `CollectionCard.jsx` | Collection preview card |
| `CollectionBuilder.jsx` | Collection creation wizard |
| `Sha256Manager.jsx` | Content-addressed file management |
| `VersionLog.jsx` | Git-style version history |
| `CommentSection.jsx` | Comments on collections |
| `P2PStatus.jsx` | P2P network status indicator |
| `WebRTCPeer.jsx` | WebRTC peer management |
| `WebRTCTransfer.jsx` | WebRTC file transfer UI |
| `ServiceStatus.jsx` | Backend service health indicator |
| `ActiveConnPanel.jsx` | Active P2P connections list |
| `PeerDetailPanel.jsx` | Peer node detail view |
| `SettingsSection.jsx` | Settings UI wrapper |
| `VisibilityPicker.jsx` | Collection visibility selector |
| `UserGroupPicker.jsx` | User/group picker for sharing |
| `PathRegistrar.jsx` | Register local file paths |
| `AnonCollectionManager.jsx` | Anonymous collection management |

### Page Map

| Page | Route | Purpose |
|------|------|------|
| `Plaza.jsx` | `/` | Collection plaza - home page |
| `Explorer.jsx` | `/:user/:coll` | User collection browser |
| `FileManager.jsx` | `/files` | File management |
| `Settings.jsx` | `/settings` | Settings page |
| `P2PPanel.jsx` | `/p2p` | P2P overview |
| `P2PDashboard.jsx` | `/p2p/dashboard` | P2P dashboard |
| `P2PTopology.jsx` | `/p2p/topology` | P2P topology graph |
| `DHTExplorer.jsx` | `/dht` | DHT routing browser |
| `IPFSPanel.jsx` | `/ipfs` | IPFS management |
| `BTPanel.jsx` | `/bt` | BT overview |
| `BTController.jsx` | `/bt/controller` | BT download control |
| `AnonCreator/` | `/anon/create` | Anonymous collection creator |
| `AnonExplorer/` | `/anon/:hash` | Anonymous collection explorer |

### AnonCreator Subcomponents

> ⚠️ **以下清单已于 2026-10-06 核实：`front/src/pages/AnonCreator/` 与
> `AnonExplorer/` 目录均已不存在**，其下列的文件也不存在。v3 重构后前端只剩
> `pages/`（8 个页面：Connect / Drive / Collections / Transfers / NodeControl / BT / IPFS / Settings）
> 与 `lib/`（nodeSession.js / swBridge.js / PeerJSConnect.jsx / pd-client/），
> 匿名集合相关功能收在 **`Collections.jsx`** 里。
> **本节保留原文仅作重构追溯**，不要照着找文件。

Directory `front/src/pages/AnonCreator/`（已删除）:

| File | Purpose |
|------|------|
| `index.jsx` | Main entry |
| `constants.js` | Constants definitions |
| `utils.js` | Utility functions |
| `LeftPanel.jsx` | Left file source panel |
| `MiddlePanel.jsx` | Middle collection content panel |
| `RightPanel.jsx` | Right edit/preview panel |
| `EditorPanel.jsx` | Text editor |
| `EditorToolbar.jsx` | Editor toolbar |
| `CollBrowser.jsx` | Collection browser |
| `CollBrowserNav.jsx` | Collection browsing navigation |
| `CollectionHeader.jsx` | Collection header |
| `CollectionRow.jsx` | Collection list row |
| `CollFileRow.jsx` | File row within collection |
| `FileSourceRow.jsx` | File source row |
| `SourceFilters.jsx` | File source filters |
| `SourceTabs.jsx` | File source tab switching |
| `SystemBrowse.jsx` | System file browser |
| `RegisteredView.jsx` | Registered files view |
| `SearchHistory.jsx` | Search history |
| `TimelineView.jsx` | Timeline view |
| `SplitHandle.jsx` | Panel split drag handle |
| `NamePrompt.jsx` | Naming dialog |
| `Toast.jsx` | Toast notification |

### AnonExplorer Subcomponents

> ⚠️ 同上：该目录已不存在，见上面 AnonCreator 节的说明。

Directory `front/src/pages/AnonExplorer/`（已删除）:

| File | Purpose |
|------|------|
| `index.jsx` | Main entry |
| `SearchBar.jsx` | Search bar (Hash input) |
| `BreadcrumbNav.jsx` | Breadcrumb navigation |
| `CollectionHeader.jsx` | Collection header info |
| `EmptyState.jsx` | Empty state placeholder |
| `FileList.jsx` | File list |
| `FileRow.jsx` | File row |
| `ImagePreview.jsx` | Image preview |
| `PdfPreview.jsx` | PDF preview |
| `TextPreview.jsx` | Text preview |
| `GenericFilePreview.jsx` | Generic file preview |
| `SingleFilePreview.jsx` | Single file preview container |
| `NestedCollectionLink.jsx` | Nested collection link |
| `Toast.jsx` | Toast notification |
| `utils.js` | Utility functions |
