# Frontend Todo List

> Last updated: 2026-04-27
> Legend: 🔴 User repeated complaints · 🟡 Done but needs verification · ⚪ To implement

---

## 1. AnonCreator (Collection Creation Page) — `react/src/pages/AnonCreator.jsx`

This is the page with the most user complaints. The core issue is the "left file source + right editing area" design is incomplete.

### 🔴 1.1 Left File Source Needs Three View Modes

**Source of complaints**: TODO.txt, MyTestResults.txt, MyTestResults2.txt, UI.txt (4 times total)

Three modes:
| Mode | Name | Function |
|------|------|----------|
| Mode 1 | 🕐 Timeline | All registered files sorted by time, with date separators |
| Mode 2 | 📁 Registered Directories | Directory tree built by provider_path, navigate level by level |
| Mode 3 | 🖥️ Local Directories | Browse server local filesystem, not dependent on registered files |

**Current status**: Mode 1,2 implemented, Mode 3 framework added (`browseRawFs` function, `rawfs` viewMode). **Need to verify if working properly**.

**Test points**:
- Switch to "Local Directories" → can see files under server `/` directory
- Can navigate into folders and return level by level
- Dragging local directory files to right editing area works

### 🔴 1.2 Create New Folder — Direct Creation, No Input Dialog

**Source of complaints**: MyTestResults.txt, MyTestResults2.txt (2 times total)

- Click "+ New Folder" → Directly create a folder entry named "New Folder"
- User double-clicks folder name → Enter rename mode
- Mobile needs long-press rename support

**Current status**: `FileTree.jsx`'s `openNewFolder` changed to directly call `onNewFolder('New Folder')`. **Need to verify if the dialog no longer appears**.

**Test points**:
- Click "+ New Folder" → Does "New Folder/" entry appear directly, no dialog
- Can double-click rename entry
- Mobile touch triggers

### 🔴 1.3 Drag Files Must Work

**Source of complaints**: MyTestResults.txt, MyTestResults2.txt (2 times total)

- Drag file from left source to right FileTree area → Add entry
- Drag file from left onto right folder → Add entry under that folder path
- Drop on empty area → Add to root directory
- Need visual feedback while dragging (highlight drop zone)

**Current status**: `FileTree.jsx` has `onDrop` handler and added `console.error` logs. **MIME type `application/peerdrive-file` set in dragStart, read in onDrop**. Need to verify matching.

**Test points**:
- Drag timeline/directory view files to right → Are they added
- Drag onto folder node → Does path include folder prefix
- Drop on empty area → Is it added
- Open browser dev tools console → Are there error logs on drag failure

### 🔴 1.4 Duplicate Adding Same Path Files Should Not Create Folder

**Source of complaints**: MyTestResults.txt, MyTestResults2.txt (2 times total)

- Users can add multiple files to the same path (different hashes)
- Multiple files with the same path in FileTree should be displayed side by side, not rendered as a folder

**Current status**: `FileTree.jsx`'s `buildTree` fixed for `_files.length > 1` case. **Need to verify**.

**Test points**:
- Add two files with same path, different hash → FileTree shows two file entries, no folder

### 🔴 1.5 Collection List Needs Full Features

**Source of complaints**: TODO.txt (1 time, but lists multiple requirements clearly)

- Collection list displayed full-screen on left
- Provide sorting by time, name, file count
- Provide search/filter by tag
- Provide search history
- Collection name: prefer `friendly_name` → `name_preview` (e.g., "File A, File B etc. 3 files") → hash prefix last

**Current status**: Sorting buttons and tag filter added, but **no search history**.

**Test points**:
- Create collection with tags → Tag filter shows corresponding tags
- Sort switching works
- Empty collections show "Empty Collection" instead of hash

### 🔴 1.6 Dialog Prompt When Name Not Set

**Source of complaints**: TODO.txt, MEMO.md (multiple times)

- Click "Save" when name is empty → `confirm()` dialog
- User selects "OK" → Call LLM to suggest name
- User selects "Cancel" → Save with empty name

**Current status**: `confirm()` dialog + `llmSuggest` call implemented.

**Test points**:
- Click save when name is empty → Does dialog appear
- Click OK → Does LLM return suggested name
- Click Cancel → Does save succeed with empty name

### 🔴 1.7 No Commit Button

**Source of complaints**: TODO.txt, commit.txt (2 times total)

- Right top bar only keeps "Save" button
- "Commit" button, "Clone and Modify" button → Already removed

**Current status**: Commit button removed, only "Save" kept. **Need to verify**.

### 🟡 1.8 "Existing Collection" → Click to Edit

**Source of complaints**: collections.txt

- Click existing collection → Load that collection's entries to editing draft (not create immediately)
- Click "Save" after editing to officially save

**Current status**: "Existing Collection" tab currently shows collection files for individual adding. Users expect to load entire collection as draft directly.

**Current behavior**: Click collection → `loadCollAsSource()` → Show file list → Click + individually
**Expected behavior**: Click collection → Load all entries of that collection to right editing area (replace existing entries)

---

## 2. FileManager (File Management Page) — `react/src/pages/FileManager.jsx`

### 🔴 2.1 Checkboxes Must Be Clearly Visible

**Source of complaints**: MyTestResults.txt, MyTestResults2.txt (2 times total)

- Each file row must have a visible checkbox (not just click color change)
- Checkboxes should be large enough (changed to w-5 h-5 + accent-cyan-500)
- Selected rows should have clear visual feedback (added blue left border + background color)

**Current status**: Implemented. **Need to verify visual effect in browser**.

### 🔴 2.2 Register Directory Creates Collection Directly

**Source of complaints**: MyTestResults.txt, MyTestResults2.txt, register.txt (3 times total)

- Select directory → Register → **Directly create anonymous collection**, don't navigate to AnonCreator
- Optionally add tags
- After registration, navigate to collection page

**Current status**: `handleRegisterCurrentFolder` changed to directly `createAnonCollection` then `navigate`. **Need to verify**.

### 🟡 2.3 Interface Should Be Larger

**Source of complaints**: MyTestResults.txt (1 time)

- Increased padding, font size, line spacing
- √ Already has "Selected X files" prompt

---

## 3. Explorer (User Collection Page) — `react/src/pages/Explorer.jsx`

### 🔴 3.1 File Browsing Uses Breadcrumb Navigation

**Source of complaints**: collections.txt, TODO.txt (2 times total)

- No recursive tree expansion
- Use level-by-level browsing: Click folder → Enter → Show contents → Return
- Top breadcrumb shows current path

**Current status**: Changed to `navIn`/`navBack` + breadcrumb. **Need to verify**.

### 🟡 3.2 Terminology Translation

- Commit → Submit
- Merge → Merge
- Don't show large "Version: N" text

**Current status**: Modified.

---

## 4. AnonExplorer (Anonymous Collection Viewer) — `react/src/pages/AnonExplorer.jsx`

### 🟡 4.1 Single File Collection Shows File Directly

- If collection has only 1 entry → Show file preview and download button directly
- Don't show directory browsing UI

**Current status**: Added `isSingleFile` detection + single file view.

### 🟡 4.2 Nested Collection Links

- If a collection entry's hash is itself another collection → Show as collection icon, click to navigate

**Current status**: Added `allCollHashes` detection. When file hash matches, renders as 📦 collection link.

### 🟡 4.3 Empty Collection Auto-Delete

- If collection entry count is 0 → Auto `deleteFile` and prompt "Empty collection, automatically deleted"

**Current status**: Added in `fetchCollection`.

---

## 5. Navbar — `react/src/components/Navbar.jsx`

### 🔴 5.1 "Explore Collections" Navigation Fix

**Source of complaints**: MyTestResults2.txt #15

- Previously used `window.location.href = '/'` ❌
- Changed to React Router `nav('/')`

---

## 6. Global UI — `react/src/pages/Plaza.jsx` etc.
