# Peerdrive Frontend Testing Documentation

> Vitest + Happy DOM + Testing Library — Unit Tests · Component Tests · Smoke Tests
> Updated: 2026-04-30

---

## Table of Contents

1. [Test Architecture](#1-test-architecture)
2. [Environment Configuration](#2-environment-configuration)
3. [Test File Organization](#3-test-file-organization)
4. [Running Tests](#4-running-tests)
5. [Writing Tests](#5-writing-tests)
6. [Existing Test Coverage](#6-existing-test-coverage)
7. [Playwright E2E](#7-playwright-e2e)
8. [Test Checklist](#8-test-checklist)

---

## 1. Test Architecture

```
┌──────────────────────────────────────────────┐
│                 Vitest (Runner)               │
│  ┌─────────────────────────────────────────┐ │
│  │         Happy DOM (Browser Env)          │ │
│  │  ┌─────────────────────────────────────┐ │ │
│  │  │    Testing Library / React           │ │ │
│  │  │    render() → screen.getByText()...  │ │ │
│  │  └─────────────────────────────────────┘ │ │
│  └─────────────────────────────────────────┘ │
└──────────────────────────────────────────────┘
```

| Layer | Tool | Responsibility |
|------|------|------|
| Test Runner | Vitest 4 | Execute tests, assertions, coverage |
| DOM Environment | Happy DOM | Simulate browser DOM API (headless) |
| React Rendering | @testing-library/react | `render()` components to virtual DOM |
| Assertion Extensions | @testing-library/jest-dom | `.toBeTruthy()`, `.toContain()`, etc. |
| Router Simulation | react-router-dom MemoryRouter | Wrap components that need router context |

### Why Happy DOM?

- Faster and lighter than jsdom
- Better support for modern Web APIs
- No extra configuration needed for Vitest integration

---

## 2. Environment Configuration

### vitest.config.ts (`front/vitest.config.ts`)

```ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],           // Handles JSX transformation
  test: {
    environment: 'happy-dom',   // Browser environment simulation
    setupFiles: ['./tests/setup.js'],  // Global setup
  },
})
```

### Global setup (`front/tests/setup.js`)

```js
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

afterEach(() => {
  cleanup()  // Unmount components after each test to prevent state leaks
})
```

### Dependency Versions

```json
{
  "devDependencies": {
    "vitest": "^4.1.5",
    "happy-dom": "^20.9.0",
    "@happy-dom/global-registrator": "^20.9.0",
    "@testing-library/react": "^16.3.2",
    "@testing-library/jest-dom": "^6.9.1",
    "@vitejs/plugin-react": "^6.0.1"
  }
}
```

---

## 3. Test File Organization

```
front/tests/
├── setup.js              # Global setup (afterEach cleanup)
├── smoke.test.jsx        # Smoke tests: core component rendering
├── components.test.jsx   # Component rendering tests: basic rendering of pages/components
└── FileTree.test.jsx     # FileTree-specific tests: state, interactions
```

### Naming Conventions

| File | Test Content |
|------|---------|
| `*.test.jsx` | Vitest unit/component tests |
| `*.smoke.mjs` | Node.js smoke scripts (direct execution) |

---

## 4. Running Tests

```bash
cd front

# Run all tests (single run)
npm test

# Equivalent to
npx vitest run

# Watch mode (auto-rerun on file changes)
npx vitest

# Run specific file
npx vitest run tests/FileTree.test.jsx

# Run tests matching a name
npx vitest run -t "FileTree"

# Generate coverage report
npx vitest run --coverage

# UI mode (visual interface)
npx vitest --ui
```

---

## 5. Writing Tests

### 5.1 Basic Pattern

```jsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import MyComponent from '../src/components/MyComponent'

// Wrapper with router context
const wrapper = ({ children }) => <MemoryRouter>{children}</MemoryRouter>

describe('MyComponent', () => {
  it('renders title', () => {
    render(<MyComponent />, { wrapper })
    expect(screen.getByText('标题')).toBeTruthy()
  })

  it('renders button', () => {
    render(<MyComponent />, { wrapper })
    expect(screen.getByRole('button', { name: '提交' })).toBeTruthy()
  })
})
```

### 5.2 Query Methods Quick Reference

| Method | Purpose | Failure Behavior |
|------|------|---------|
| `screen.getByText('xxx')` | Exact text match | Throws if not found |
| `screen.getByPlaceholderText('xxx')` | Find by placeholder | Throws if not found |
| `screen.getByRole('button', { name: 'xxx' })` | Find by ARIA role | Throws if not found |
| `screen.queryByText('xxx')` | Text match | Returns null if not found |
| `container.textContent` | Get all text within element | - |

### 5.3 Components Without Router

For pure display components that don't depend on routing, simply use `render()`:

```jsx
import { render } from '@testing-library/react'
import FileTree from '../src/components/FileTree'

it('renders empty state', () => {
  const { container } = render(
    <FileTree entries={[]} entryActions={{}} />
  )
  expect(container.textContent).toContain('拖拽')
})
```

### 5.4 Components Requiring Context

If a component consumes AppContext or PageContext, wrap it with Provider in tests:

```jsx
import { AppContext, PageContext } from '../src/App'

function Wrapper({ children }) {
  return (
    <AppContext.Provider value={{ username: 'test', setUsername: () => {}, nodeInfo: null, setNodeInfo: () => {} }}>
      <PageContext.Provider value={{ pageContext: null, setPageContext: () => {} }}>
        <MemoryRouter>{children}</MemoryRouter>
      </PageContext.Provider>
    </AppContext.Provider>
  )
}

render(<MyPage />, { wrapper: Wrapper })
```

### 5.5 Testing Principles

1. **Rendering tests first** — Each component should at least render without crashing
2. **Key text assertions** — Check if titles/buttons/placeholders appear
3. **Boundary states** — Empty data, loading, error states
4. **Don't test implementation details** — Don't test internal state values, don't test CSS class names
5. **Isolation** — Each test should not depend on state from other tests

---

## 6. Existing Test Coverage

### Smoke Tests (`tests/smoke.test.jsx`)

Ensure core components don't crash:

| Component | Assertion |
|------|------|
| `Sha256Manager` | Title "SHA256 寻址" |
| `CollectionBuilder` | Title "合集构建器" + "浏览合集" |
| `P2PStatus` | Title "P2P 网络" |
| `App` | Logo "Peerdrive" |

### Component Tests (`tests/components.test.jsx`)

Cover rendering of main pages and components:

| Test Target | Test Count | Key Assertions |
|----------|--------|---------|
| `App` | 1 | Peerdrive logo in Navbar |
| `Plaza` | 3 | Title/Create button/Search box |
| `AnonCreator` | 3 | 4-Tab bar/Save button/No Commit button |
| `AnonExplorer` | 1 | Hash input box |
| `FileManager` | 1 | Title "文件管理" |
| `Settings` | 1 | Title "设置" |
| `VersionLog` | 1 | Empty state message |
| `Navbar` | 1 | Navigation links |

### FileTree-Specific Tests (`tests/FileTree.test.jsx`)

| Test | Coverage Scenario |
|------|---------|
| Empty state rendering | `entries=[]` → displays "拖拽" |
| Flat item rendering | Single file → displays filename |
| Nested path expansion | `d/a.txt` → displays directory "d" |
| New folder button | Confirm button text appears |

---

## 7. Playwright E2E

### Smoke Script (`tests/playwright-smoke.mjs`)

This is a standalone Node.js script for end-to-end validation — connects to a browser running on the Windows host machine (CDP port 9222), testing real page interactions.

```bash
# Prerequisite: Windows host Chrome/Edge started with debug port
# chrome.exe --remote-debugging-port=9222 --remote-debugging-address=0.0.0.0

cd front
node tests/playwright-smoke.mjs
```

This script requires the Playwright skill (`playwright-test`) to run. See the corresponding skill documentation.

---

## 8. Test Checklist

### Core Page Rendering

- [ ] `App` — Full layout rendering
- [ ] `Plaza` — Plaza loading, collection card display
- [ ] `FileManager` — File list display
- [ ] `Explorer` — Collection detail display
- [ ] `AnonCreator` — Three-column layout rendering
- [ ] `AnonExplorer` — Collection browsing rendering
- [ ] `Settings` — Settings group rendering
- [ ] `P2PPanel` — P2P status display
- [ ] `IPFSPanel` — IPFS panel rendering
- [ ] `BTPanel` — BT panel rendering

### Component-Specific

- [ ] `FileTree` — Empty/Flat/Nested/Multiple files
- [ ] `Navbar` — Search panel open/close, keyboard navigation
- [ ] `MobileNav` — Mobile link rendering
- [ ] `LLMAssistant` — Chat window rendering
- [ ] `VersionLog` — Version list/Empty state/Rollback button
- [ ] `CommentSection` — Comment list/Post
- [ ] `CollectionCard` — Card content display
- [ ] `VisibilityPicker` — Visibility options
- [ ] `WebRTCTransfer` — Transfer progress display

### Interactions & State

- [ ] User input → state update
- [ ] API call success → UI update
- [ ] API call failure → error message
- [ ] Loading state → skeleton screen/loading indicator
- [ ] Empty data state → empty state message
- [ ] Route navigation → page switch

### Suggested Tests to Add

The following tests have not been implemented yet and are recommended by priority:

1. **API mock tests** — Use `vi.mock()` or `msw` to mock backend responses, test loading and error states
2. **User interaction tests** — Use `fireEvent` or `@testing-library/user-event` to test button clicks, form inputs
3. **AnonCreator interactions** — Test three-column drag-and-drop, file selection, collection save flow
4. **Snapshot tests** — Snapshot comparison of key components to prevent unexpected UI changes

---

## Appendix: Common Assertions Quick Reference

```jsx
// Existence
expect(screen.getByText('标题')).toBeTruthy()
expect(screen.queryByText('不应存在')).toBeNull()

// Text content
expect(container.textContent).toContain('部分文本')
expect(element).toHaveTextContent('精确文本')

// Attributes
expect(input).toHaveValue('test')
expect(link).toHaveAttribute('href', '/target')

// Visibility
expect(element).toBeVisible()
expect(element).not.toBeVisible()
```
