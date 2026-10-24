# Task: Clean up the residual Chinese in peerdrive (i18n or English)

> ## ⚠️ 2026-10-04 状态更正 —— 本文描述的损坏状态已全部回退
>
> **本文写于 2026-10-03，描述的是那批"中译英"提交（`0713b38..93889b1`）的中间状态。
> 该批已被审计并部分回退，本文所列的"功能消失/权限失效"问题现在都不存在了。**
>
> | 本文所说的损坏 | 现状 |
> |---|---|
> | 面板分享链接出口整块消失（§6 表格 10 项 missing） | **已恢复**，回退于 `ef53fe2` |
> | `id: myId` 被删、private 内容永远取不到（§6.1） | **已恢复** |
> | id 前缀 `pd-panel-` → `p-`（§6.1） | **已恢复**，前缀仍是 `pd-panel-` |
> | SHA-256 K 表少一个常量（§3.2） | **从来不是这次的问题**——见下方"两条需要更正的陈述" |
> | 建议 `git checkout HEAD -- panel/app.js` 回退（§6.2） | **已执行** |
>
> **契约测试已恢复全绿**：`packages/peerdrive-client` 从 13 失败 → **0 失败（115/115）**，
> CI（Peerdrive CI / Go Build Matrix / E2E）当前全绿，HEAD `75cfdb3`。
>
> **现在要做的**：本文 §2/§4/§5 里那些**产品决策**仍然有效且没做过
> （i18n 怎么做、archive 文档翻不翻、面板 4 段提示块怎么处理）。
> §3（"我已修好的两件事"）与 §6（"面板功能消失了"）**已过期**，读 §0 的更正即可。
>
> **动手翻译前先读 [`doc/TRANSLATION-CONSTRAINTS.md`](doc/TRANSLATION-CONSTRAINTS.md)**
> ——那批提交之所以越界改坏 16 个文件，正是因为没有这份约束。
>
> ### 两条需要更正的陈述（否则会误导下一个人）
>
> 1. **§3.2 的 K 表从未被改坏。** 本文称 K 表从 64 项变成 63 项（`0xf40e3585` 被删）。
>    实测全历史：`git log --all -- packages/peerdrive-client/src/sha256.js
>    front/src/lib/pd-client/sha256.js` 的**每一个提交**里，两处 K 表都是完整 64 项、
>    都含 `0xf40e3585`。§3.1 的单引号串语法错误是真的（`peerdrive-client` CI 确实抓到过）。
> 2. **§0.1 与 §5 的产品判断有一个前提写歪了**：§2.2 用"零依赖是这个包的卖点"
>    推出"panel 禁止引任何 i18n 库"。若真实诉求只是**页面打开时不额外下载**，
>    正确结论是**把词条内联进产物**，"不许引库"只是它的副产品而不是目标。
>    这一点尚未有产品侧结论，留给接手的人判断。

> Handover document. Written for the next person (or agent) who picks this up.
> Generated: 2026-10-03. Repo: `D:\WorkPlace\peerdrive`, branch `refactor`.
> **Read before touching anything**: §0 — the workspace currently holds **someone else's unfinished English-ization redo**, not a clean starting point.

---

## 0. Understand what you are taking over first (most important)

Someone (another agent, or yourself in a previous turn) has already been doing an **English-ization redo** in the workspace, **stopped halfway, and broke two files**.
I fixed those two files (see §3), but I **did not finish the remaining part on their behalf** — that part needs product decisions, see §5.

### 0.1 Current changes under `packages/peerdrive-client` (uncommitted, visible in `git status`)

```
 M README.md              345 lines changed
 M demo/format.js           7 lines
 M panel/app.js          1649 lines  ← big rewrite +695/−954, a large chunk of functionality was deleted
 M src/client.js           454 lines
 M src/index.js             10 lines
 M src/protocol.js         162 lines
 M src/sha256.js            51 lines
```

### 0.2 English-ization of `front/` (React admin panel) is **already nearly complete**

| | HEAD | Workspace |
|---|---|---|
| Number of files with Chinese in `front/src` | **22** | **2** |

Only two left:
- `front/src/index.css` (10 lines) — **entirely CSS comments**, not UI copy, changing them is optional.
- `front/src/pages/Transfers.jsx` (12 lines) — **the only remaining real UI Chinese**.

So you basically don't need to redo English-ization of `front/` from scratch — finish `Transfers.jsx` + lay down the i18n skeleton and you're done.

---

## 1. Chinese classification: first decide what to touch

Results of the last whole-repo scan (excluding `node_modules` / `.git` / `.workbuddy`):
**192 files contain Chinese, 156 of them tracked by git**. Grouped into four buckets by how you should handle them:

| Class | Size | Approach | Priority |
|---|---|---|---|
| **A. Product-facing UI** (user-visible) | 9 files | **Must be i18n** (see §2) | High |
| **B. Documentation markdown** | 83 files | Recommend English, but **do not do them all at once** (see §4) | Low |
| **C. Tests** | 30 files (13 Go + 17 JS) | English (assertion messages, test names) | Medium |
| **D. Scripts / CI / Makefile / .env.example** | 14 files | English (comments are enough) | Medium |
| ~~E. Root-level txt~~ | ~~6~~ | **Handled** (archived into kb then deleted; only `doc.txt` / `todo.txt` remain) | — |

---

## 2. Class A: Product-facing UI — must be i18n, do not just translate

### 2.1 `front/` (React + Vite, 8 pages + 1 component)

- Current state: **UI copy is 95% English**, only 12 lines of `Transfers.jsx` left.
- Dependencies: `react` 19 / `react-dom` / `react-router-dom` 7 / `peerjs`. **No i18n library**.
- Recommended approach:
  1. Create `front/src/i18n/`: `en.js` / `zh-CN.js` entries + a single `t(key, params)` + React context.
  2. Group entries by page (`pages.Drive.*` / `pages.NodeControl.*` …); do not flatten.
  3. Persist language choice in `localStorage`, default to following `navigator.language`.
  4. Put a language switcher in the top nav (`Settings.jsx` or the header).
- ⚠️ **The English-ization redo is still uncommitted in the workspace** — when adding i18n, work from the workspace version, **not HEAD**. Run `git diff` first to see what's there.

### 2.2 `packages/peerdrive-client/panel/` (public panel) — **no third-party libraries allowed**

This is a **zero-runtime-dependency** single-file artifact (`dist/panel.html`, openable via `file://`, droppable into any static host).
**This is the single biggest selling point of this package** (the README hammers it over and over); pulling in any i18n library would break it.

- The artifact is stitched together by `scripts/build-panel.mjs`: a hand-written `transpile()` strips `import`/`export`,
  inlines `sha256.js ← protocol.js ← client.js` in topological order, then injects them into the
  `/*__PANEL_BUNDLE__*/` and `/*__PANEL_APP__*/` placeholders in `panel/template.html`.
- **Recommended approach**:
  1. Create `panel/i18n.js` (not under `src/`, because `src/` is the protocol implementation, and UI copy doesn't belong to the protocol).
  2. Inline entries as `var I18N = { en: {...}, 'zh-CN': {...} }` + `t(key)` + language persistence.
  3. Add a `/*__PANEL_I18N__*/` injection point to `build-panel.mjs` (or fold it into a standalone script right before `__PANEL_APP__`).
  4. **The 4 long hint blocks in `template.html` need a decision** (see §5.1).
- ⚠️ After any change you **must run `npm run build:panel`** to regenerate `dist/panel.html`; CI's `check:panel` blocks drift.
- ⚠️ The panel is an **IIFE**; `var $ = …` is declared later on. Early in startup you can only call `persistMyId` (disk write),
  **touching the DOM throws `$ is not a function` and aborts the whole IIFE** (the entire page fails, `window.__panel` doesn't even exist).
  If i18n initialization needs to read or write the DOM, put it inside `boot()`.

---

## 3. Two things I already fixed (do not revert)

### 3.1 Syntax error in `src/client.js`

```js
// Broken: bare apostrophe "PeerJS's" inside a single-quoted string closes it early → SyntaxError for the whole file
throw new TypeError('peerdrive-client: connectToPeer requires PeerJS's Peer constructor')
// Fixed to be wrapped in double quotes
```

The line number reported by `node --check` (1001:73) points at this `throw` statement itself, but that line is **syntactically correct** —
the real problem is the apostrophe inside the string. What `node` reports is "where the engine gave up", not "where the problem is".
Use acorn for authoritative diagnosis:

```bash
node -e "const acorn=require('acorn'),fs=require('fs');
try{acorn.parse(fs.readFileSync('src/client.js','utf8'),{ecmaVersion:'latest',sourceType:'module'})}
catch(e){console.log(e.message, e.loc)}"
```

### 3.2 The K constant table in `src/sha256.js` was missing one entry (**severe**)

```js
// Broken: K table has 63 entries (HEAD has 64) — 0xf40e3585 dropped
0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0x106aa070,
// 0xf40e3585 restored
```

**The consequence is far worse than a syntax error**: from this entry onward SHA-256 is completely misaligned, **even the empty-input hash is wrong**
(`dbe79673...` instead of the NIST-standard `e3b0c442...`).
This causes **every content-addressed check to fail** — every downloaded file is `HASH_MISMATCH`, the netdisk is unusable.
**Sanity check**: the `K` table must have exactly 64 entries; add an assertion to prevent recurrence:

```bash
node -e "const s=require('fs').readFileSync('src/sha256.js','utf8');
console.log(s.match(/const K = new Uint32Array\(\[([\s\S]*?)\]\)/)[1].split(',').filter(x=>x.trim()).length)"  # must be 64
```

> Lesson learned: **during the English rewrite, one hexadecimal constant was casually deleted — completely harmless-looking on the diff**.
> Any change to `sha256.js` must run the `sha256/known vectors` tests inside `npm test` (NIST standard values).

---

## 4. Class B: Documentation (83 files) — recommended, but don't do them all at once

- Distribution: `doc/archive/` 51 · `doc/testing/` 12 (incl. archive) · `doc/design/` 10 · rest 10.
- **`doc/archive/` is a historical archive; it exists "only for decision traceability, not to be followed"**.
  Translating it has the lowest value (nobody is going to read a 2026-05 archive). **Recommendation: keep archive in Chinese, only translate the active docs.**
- About 30 active docs: `doc/NETDISK.md` / `doc/REFACTOR.md` / `doc/ROADMAP.md` / `doc/AGENTS.md` / `README.md` …
- ⚠️ **The `doc/README.md` index and `doc/FILE-REFERENCE.md` must be updated in sync**, otherwise the entry points still point to old locations.
- ⚠️ After changes, run `.workbuddy/check-doc-links.py` to scan all md links in the repo, and confirm **0 broken links in active docs**
  (broken cross-references inside archive are not fixed).

---

## 5. Three things you (product decisions) need to settle first

### 5.1 How to handle the 4 long hint blocks in the panel

`template.html` has 4 large Chinese explanation blocks (node-connection caveats, ingest instructions, etc.), totaling about 20 lines.
- **A. Put them all into the entry table** → they follow language switching; but the entry file becomes large, and these strings describe protocol details, so translations easily lose fidelity.
- **B. Keep them in Chinese, only translate UI labels** (buttons, titles, labels) → small entry table, low risk, but English users can't read the instructions.
- **C. Split them**: core warnings (PSK_REQUIRED / SSRF rejections are expected behavior) go into the entry table, protocol details stay as comments.

**I lean toward C**, but this is your call.

### 5.2 Are there existing English strings in `front/` that can be reused

That English-ization redo in the workspace has already translated the UI copy of 20+ files. If that part is to be kept (rather than `git checkout` to restore),
those English strings are a ready-made source for the en entries — extracting them directly is more accurate than retranslating.

**Decide this first**: `git diff front/src` to see what those 20 files changed, then decide keep or restore.

### 5.3 The 1649-line change in `panel/app.js`: keep or restore

**The current workspace `app.js` deleted an entire block of functionality** (not copy — behavior). See §6.

---

## 6. ⚠️ Most important handover item: the panel's share-link functionality disappeared entirely

**10 of the 13** assertions in `test/panel-contract.test.mjs` **fail**, because after the rewrite these features **simply do not exist** in `panel/app.js`:

| Feature required by the contract tests | In the new app.js |
|---|---|
| `data-link=` manifest file "Link" button | **missing** |
| `data-clink=` manifest collection "Link" button | **missing** |
| `data-coll-link=` whole-collection link | **missing** |
| `data-lsave=` / `data-lpeek=` / `data-llink=` per-entry save/preview/re-share for collection entries | **missing** |
| `id="btn-linked-get"` unlisted standalone fetch entry | **missing** |
| `async function resolveLinked(` link content identification | renamed to `fetchLinked`, and **has no protection beyond 64-hex validation** |
| `linkedFor` bookkeeping (dedupe requests) | has `linkedFor`, but not kept in sync with the new `maybeFetchLinked` |
| `execCommand('copy')` clipboard fallback path (often rejected under file://) | **missing** |
| `id: myId` passed into peerOptions | **missing** (**private content can never be retrieved**) |
| `unavailable-id` collide-and-retry id swap | **missing** |

### 6.1 Two additional behavior changes (not bugs, but you need to decide whether they're wanted)

- **id prefix changed**: `pd-panel-` → `p-` (with a timestamp suffix). The contract tests assert the former.
  Once the prefix changes, old ids saved in users' browsers no longer match the old ids in operators' friend lists — **private permissions silently stop working**.
- **The `KIND` table still has Chinese**: `{ get: 'Download', put: 'Local ingest', pull: 'Network ingest' }` (the only residual Chinese in `app.js`).

### 6.2 My recommendation

**First `git checkout HEAD -- panel/app.js` to restore, then do the i18n on top of HEAD.**

Reason: the current version deleted an **unlisted share out-port that was already shipping** — which is one of the core selling points of this panel
(a feature users specifically requested in the previous `😅.txt` feedback round).
Adding i18n on top of a feature-missing base is i18n-ing a half-built product.

If you insist on keeping the redo version, the 10 contract tests either have to be rewritten (accepting the features are gone), or the functionality has to be restored (much more work).

---

## 7. Verification checklist (must run after changes)

```bash
cd D:/WorkPlace/peerdrive/packages/peerdrive-client
npm test                      # Current baseline: 115 cases / 102 pass / 13 fail (all in the panel contract)
npm run build:panel           # Required after touching panel/; otherwise CI's check:panel blocks the drift
npm run check:panel           # Confirm the artifact matches the source

cd D:/WorkPlace/peerdrive/front
npm test                      # vitest
npm run build

cd D:/WorkPlace/peerdrive
node .workbuddy/check-doc-links.py    # After doc changes
```

**End-to-end (mandatory after touching panel/)**: `scripts/verify-panel.mjs` needs real hardware (a running node + self-hosted signaling
+ playwright-core + Edge/Chromium). Chinese copy is **not** its selector (it uses ids like `#btn-connect`),
so changing copy does not break it — but changing `id` does.

---

## 8. One-sentence summary

**I fixed the foundation (SHA-256 K table + the syntax error), but the product decisions are yours (keep or restore that redo).
The missing share-link functionality must be assigned ownership first, otherwise i18n gets built on top of a partial product.**
