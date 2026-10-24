# Chapter 4: Freely Choose What to Share

> Continues from Chapter 3: you already know there are three tiers — public / unlisted / private.
> This chapter solves "which content is **visible to others**, and which tier to assign" — check items one by one, grant what you want to grant, no node restart required.
> **No compilation needed throughout**: the admin console uses button clicks, and the command line uses `curl` against the local HTTP server.

---

## 4.0 Remember One Thing First: Holding ≠ Sharing

The table at the end of Chapter 2 continues to apply here (levels described in Chapter 3), but this chapter makes it actionable:

- Files in your content store → **only you can see them**;
- Files in the share list → **any connected peer can see and retrieve them** (via the `share` frame).

The share list is **empty** by default (`PEERDRIVE_SHARE_ENABLE=false`), meaning: if you do nothing, the files on your node are completely invisible to others. This is not a limitation — it's default protection.

---

## 4.1 Method One: Check in the Admin Console (Recommended)

Open the admin console (the `front/` installed in Chapter 1 §1.5) → **My Drive**:

1. There's now a share bar at the top:
   - Left side is the **master switch** (`Sharing to outsiders: off` → click to change to `Sharing to outsiders: on`);
   - Middle displays `N / M files shared`;
   - Below that is the three-tier level description + **friend node ID** input box (who `private` is granted to — see Chapter 3);
2. Each row in the file table now has a **Share / Unshare** button;
3. Already-shared rows also show a **level dropdown** (public / unlisted / private) — unshared rows don't show it, so you don't think "selecting a level means it's already shared out";
4. Takes effect immediately upon clicking — the button text changes on the spot, with a top-right notification `Shared xxx.txt`.

> Turning off the master switch **does not clear your checks**: it just makes the external list empty; when you turn it back on, your previous checks are still there.

### Sharing Directories (Share an Entire Directory)

Below the "share level / friends" section there's a **Shared Directories** entry: fill in an absolute path → select level → **Add Directory**.

- It includes **all files** under that directory (including any added later) — no need to check them one by one;
- Added directories are listed below, each can individually change level or be **removed**;
- Files brought in by a directory show in the file table below marked "brought in by shared directory — to remove, change directory scope" — meaning to remove them, you must remove the directory here, not click that row;
- The volume root (`/`, `C:\`) will be rejected by the backend (400), with the error shown directly on the page.

> The `dirs` field in the backend `PUT /peerjs/share` is a **full replacement**: the admin console sends back all existing directories on every add/remove, so you won't encounter "added one directory and another quietly disappeared."

### What It Looks Like on the Consumer Side (Public Panel)

After sharing, when someone opens your node with the **public panel** (the `panel.html` from Chapter 1) they'll see:

- Each row in the share list has **Save / Preview / Link** buttons;
- "Link" generates a `?node=<node-id>&hash=<64hex>&auto=1` link — this is the **landing action for unlisted** mentioned in Chapter 3 — not listed, but retrievable by hash;
- The **My Node ID** in the top-right of the panel (starts with `pd-panel-`, fixed) is the value you need to fill into their friend list, otherwise they can't get `private` content.

### Two Kinds of "Already Shared" — The One You Can't Uncheck Is Normal

A file in the table may come from one of two sources:

| Source | How it got there | Can it be individually unchecked? |
|---|---|---|
| Single file check | You clicked "Share" on this row | Yes, click "Unshare" again |
| Directory sharing | Its entire directory was shared | **No** — you must change the directory scope |

Rows brought in by directory sharing show a hover tooltip "brought in by shared directory — to remove, change directory scope." This is intentional: otherwise "unchecked a file, but it reappeared after restart" would become a ghost story.

---

## 4.2 Method Two: Command Line (Scripts / No Browser)

Three endpoints, node on `3001`:

```bash
# See current share scope + optional file list (each row includes shared / by_dir / level)
curl -s http://127.0.0.1:3001/peerjs/share | python -m json.tool

# Turn on master switch (only send fields to change, others stay as-is)
curl -s -X PUT http://127.0.0.1:3001/peerjs/share \
  -H 'Content-Type: application/json' -d '{"enable":true}'

# Check two files (by hash). Omitting level = keep existing level (defaults to public if none)
curl -s -X POST http://127.0.0.1:3001/peerjs/share/files \
  -H 'Content-Type: application/json' \
  -d '{"hashes":["<64-digit hex>","<64-digit hex>"],"shared":true}'

# Share an entire directory, set the whole directory to "unlisted" (level details in Chapter 3)
curl -s -X PUT http://127.0.0.1:3001/peerjs/share \
  -H 'Content-Type: application/json' \
  -d '{"dirs":[{"id":"/home/me/media","level":"unlisted"}]}'
```

> Entries can also be plain strings (`"dirs":["/home/me/media"]`, level defaults to public) — the backend accepts both formats; the string form is for backwards compatibility with previously persisted scope files.

`GET` returns something like this (excerpt):

```json
{
  "enable": true,
  "levels": ["public", "unlisted", "private"],
  "dirs": [],
  "collections": [],
  "friends": [],
  "files": [
    {"hash":"<64-digit hex>","name":"upload-me.txt","size":1234,"shared":true,"by_dir":false,"level":"public"}
  ],
  "selected": [{"id":"<64-digit hex>","level":"public"}],
  "summary": {"collections":0,"files":1,"dirs":0}
}
```

One-line field descriptions:

| Field | Meaning |
|---|---|
| `enable` | Master switch. Off = empty external list, checks preserved |
| `dirs` / `collections` | Share scope by directory / by collection (entries include `level`) |
| `files[]` | Optional file list (**candidates**), each row includes whether shared and effective level |
| `selected` | Hashes you've checked. **Includes those no longer in the index** (e.g. file deleted), so you can see "I checked this" |
| `friends` | Friend node ID list (`private` is granted to these — see Chapter 3) |
| `levels` | Three tier values, for UI dropdown rendering |
| `summary` | Entry count the peer's `share` frame will see (calculated from **anonymous perspective**, excluding friend-only items) |

---

## 4.3 Three Granularities: Share What You Want

| Want to share | Use this |
|---|---|
| One specific file just uploaded | Admin console file row "Share" button, or `POST /peerjs/share/files` (by hash) — **does not require it to be in any shared directory** |
| Entire photo directory (including future additions) | Admin console "Shared Directory", or `PUT {"dirs":["/path/to/photos"]}` |
| A packaged collection | `PUT {"collections":["<hash>"]}`, or `"all"` = all public collections |

The three sources take **union**, non-interfering: checking single files, directory sharing works as normal; unchecking a single file, the directory-brought-in copy remains (see §4.1 table).

> Restricted / private collections won't be shared even if written into `collections`: the `share` frame doesn't carry requester identity, and without identity, the access list can't be verified — sharing them out is equivalent to making "restricted to specified accounts" content public.

---

## 4.4 Still There After Restart? Yes

Your selections are stored in the node's `PEERDRIVE_STORAGE/share_scope.json`.

The `PEERDRIVE_SHARE_ENABLE` / `SHARE_DIRS` / `SHARE_COLLECTIONS` environment variables are only **initial values on first startup**: after seeding once, the file takes precedence. So:

- Shares you unchecked in the admin console will **not resurrect on restart**;
- Conversely, changing environment variables won't override your existing selections (to start over, delete that JSON file and restart).

---

## 4.5 How to Verify "Others Can Actually See It"

**Most direct verification**: use another node (or the panel from Chapter 1) to send a `share` frame.

```bash
# On another node: ask this node what it's sharing
curl -s http://127.0.0.1:3001/peerjs/nodes/<peer-peer-id>/shares | python -m json.tool
```

The returned `files` / `collections` are what you checked. If not connected, it will actively dial out and wait a few seconds; if still unreachable, it reports 502 (debug per Chapter 1 §1.6).

**Chapter Self-Check List**:

1. Master switch off → peer `shares` returns `files: []`, `collections: []`;
2. Check a file → it appears in peer's list;
3. Uncheck → it disappears from peer's list;
4. Restart node → checks remain (§4.4);
5. Add a shared directory → files in that directory (including new ones) are visible to peers, file rows marked "brought in by shared directory"; remove directory → they all disappear together.

---

## 4.6 Edge Cases & Two "Errors" That Are Actually Protection

| Symptom | Cause |
|---|---|
| `PUT {"dirs":["/"]}` returns 400 | Volume root can't be a shared directory — that's equivalent to sharing the entire disk. Fill in a specific subdirectory |
| `POST` with wrong hash returns 400 | Only 64-digit hex sha256 accepted; entire batch rejected, no partial writes |
| File checked, but peer still can't see it | ① Master switch not on; ② Peer is asking for a cached old list, wait for its next `share` |
| List shows it, but peer fails to pull | Directory not registered as a readable root (admin console paths auto-register; manually configured env-var directories must be set up at startup) |

> To understand the design and pitfalls behind these checks, see `doc/NETDISK.md` §12 and §11 (path boundaries).
> The next chapter ([Chapter 5](05-save-from-other-nodes.md)) goes the other direction: things others share with me — how to save them into my own node (the panel's "Save" and the admin console's "Save Selected" are not the same thing).
