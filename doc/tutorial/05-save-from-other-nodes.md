# Chapter 5: Cross-Node Save — Storing Others' Content on Your Own Node

> Continues from Chapter 4: you already know how to decide "what to share and who can see it."
> This chapter goes the other direction: **I am the consumer** — how to pull content from another node into my own node.
> **No compilation needed throughout**: the admin console uses button clicks, and the command line uses `curl` against the local HTTP server.

---

## 5.0 Distinguish Three Kinds of "Save" First — Don't Click the Wrong One

This is the most confusing part of this chapter: **"Save" means two different things** in the panel and the admin console.

| Entry Point | Button | Where It Lands | Who Can Use |
|---|---|---|---|
| Public Panel (`panel.html`) | "Save" | **The computer you opened the panel on** (browser download directory) | Anyone, no node required |
| Admin Console → My Node → Other Node | "Save Selected" / "Save Entire Collection" | **Your own node** (some directory on disk) | Must have a running node first |
| Panel → URL input box | "Ingest from Network" | The node you're connected to (node fetches this URL) | See §5.7 |

One-line mnemonic: **the panel is a "temporary visitor," it has no storage of its own** — the panel's Save drags files back to your machine; to land in "My Drive," a node must be receiving for you, and the admin console is that node's remote control.

> So: to have files appear in "My Drive" and be shareable again (Chapter 4), use the admin console path; if you just want to download a copy to your computer, the panel is enough — the two paths don't conflict.

---

## 5.1 Admin Console: Check → Save Selected

1. Left sidebar **My Node** (`/peers`) → click a connected node to enter its detail page;
2. The page lists what the peer has shared with you: **individual files** and **collection entries** are in one table, each row can be checked;
3. Check several rows → click **Save Selected (N)**;
4. Immediately jump to the **Transfer Tasks** page to see progress (tasks run in the node's backend, browser can be closed and they continue).

Collections can also be saved as a whole: each collection card has a **Save Entire Collection** button, creating N tasks at once.

> Creating tasks individually rather than sending one "batch request" is intentional: each file has **independent progress, independent success/failure** — one bad file shouldn't stop the whole batch (same semantics as "select multiple files to download" in a BT client).

---

## 5.2 Why "Save Entire Collection" Sometimes Returns 404

After clicking, the node will **immediately** request a share list (`share` frame) from the peer, looking for this collection there; if not in the list, it falls back to **fetching the manifest by hash** (a collection's manifest itself is a content-addressed JSON stored by hash — so unlisted collections can also be saved as a whole, they're just not in the list, not non-existent). If both paths fail, it errors:

- Peer is **offline / unreachable** → can't get the list or the manifest → `502`;
- The collection is **private and you're not on their friend list** → not in the list, and the manifest is also blocked → `404` (not 403: things not in the list, the server won't tell you "it exists but not for you");
- The hash you filled in **isn't a collection at all** (e.g. you passed a file's hash as a collection hash) → the manifest can't be parsed into entries → also `404`.

> Individual saves don't have this limitation: `POST /p2p/pull` works by hash, doesn't look at the list, so after getting a collection link, you can pull entry by entry hash (you just have to assemble paths and names yourself).

> Note it **doesn't trust the quantity summary displayed on the page**: that number is just guidance, possibly a cached value from minutes ago; the actual save always goes by the peer's current state.

---

## 5.3 Transfer Tasks Page: Progress, Status, Cancel

Left sidebar **Transfer Tasks** (`/transfers`), looks like a download tool:

| Status | Meaning | What to Do |
|---|---|---|
| `running` | Transferring, with progress bar and rate | Can click "Cancel" |
| `done` | Complete, end of row shows `Saved to <path>` | — |
| `failed` | Failed, error reason shown inline | See §5.8 |
| `cancelled` | You (or peer disconnect) cancelled | Try again |

Several behavioral details (all "looks like a bug, actually design"):

- **Only polls when there are `running` tasks** (once per second), stops when all finish, doesn't keep hitting the backend idly;
- Finished tasks **stay in the list** (so you can look back at "where it was saved"), just sorted to the bottom; the backend task table limit is 200 entries, overflow auto-drops the oldest finished tasks;
- **Cancel only works on `running`**: finished tasks return an error (the button is effectively unclickable); cancel is "close the stream + wake the reading goroutine," not delete the file;
- **Locally existing content with the same hash** is skipped directly (`skipped`), status remains `done` — content-addressed dedup, same content doesn't need downloading twice;
- Maximum **3** concurrent transfers: each pull occupies one WebRTC connection, the peer also serves serially per connection, more just makes each slower;
- The task table is **in memory only**: after restarting the node, the transfer page is empty, but already-saved files remain on disk (there's no "resume" concept — reopening a task starts from scratch; identical content will be `skipped` directly).

---

## 5.4 Command Line Version (Scripts / No Browser)

Node on `3001`, four endpoints:

```bash
# ① Save one of the peer's files (hash must be 64-digit hex)
curl -s -X POST http://127.0.0.1:3001/p2p/pull \
  -H 'Content-Type: application/json' \
  -d '{"peer":"<peer-peer-id>","hash":"<64-digit hex>","name":"a.txt"}'

# ② Save an entire collection
curl -s -X POST http://127.0.0.1:3001/p2p/pull/collection \
  -H 'Content-Type: application/json' \
  -d '{"peer":"<peer-peer-id>","collection":"<collection-hash>"}'

# ③ See task list (newest first)
curl -s http://127.0.0.1:3001/p2p/pull | python -m json.tool

# ④ Cancel a specific task
curl -s -X POST http://127.0.0.1:3001/p2p/pull/cancel \
  -H 'Content-Type: application/json' -d '{"id":"<job id>"}'
```

Return examples (excerpts from ① and ③):

```json
{
  "job": {
    "id": "j-1a2b3c", "peer": "pd-alice", "hash": "<64-digit hex>", "name": "a.txt",
    "total": -1, "received": 0, "status": "running", "started_at": "2026-09-22T13:00:00Z"
  }
}
```

One-line field descriptions:

| Field | Meaning |
|---|---|
| `total` | Size declared by peer; **`-1` = not declared**, progress bar shows indeterminate state |
| `received` | Bytes received |
| `path` | Relative path within the collection (saved locally with same structure) |
| `skipped` | Same content already exists locally, was skipped |
| `saved_to` | Final absolute path on disk (only on `done`) |
| `error` | Failure reason (only on `failed`) |

> `POST /p2p/pull` only needs the correct hash and the peer willing to give it — **the file doesn't need to be in the peer's share list** — so it's the command-line counterpart of Chapter 3's `unlisted` (not listed but downloadable by hash).
>
> Note `POST /p2p/pull` returning 200 **only means the task was created**, not that the transfer succeeded: parameter-level errors (empty `peer`, hash not 64-digit hex) return 400 immediately, but in-transfer failures (peer offline, private) are written asynchronously into the task — check via ③.

---

## 5.5 Where Was It Saved? How Is Integrity Guaranteed?

Save location is fixed:

```text
<PEERDRIVE_DOWNLOAD_DIR>/pulled/<relative-path>
```

- `PEERDRIVE_DOWNLOAD_DIR` defaults to `./downloads` (the directory used to start the node in Chapter 1);
- Single files are named whatever `name` you passed; collection entries keep the peer's **directory structure**;
- If the peer didn't provide a name → uses `hash[:12]` as the filename, at least lands on disk.

**Writes follow "temp file → verify → rename" in three steps**:

1. Stream-write into `<formal-name>.part` **in the same directory** (same directory guarantees the final rename is atomic — no "half an a.txt");
2. Calculate **sha256** while writing, if it doesn't match the hash → delete the temp file, task reports `failed: hash mismatch` (this step can't be skipped: otherwise it's equivalent to allowing the peer to write arbitrary content to your disk);
3. Only after passing does it `rename` to the formal name and **register into the file index** — after which "My Drive" will show it, and others can retrieve it from you.

Two exception messages — don't misread them:

- `done` but with `saved but not indexed`: the file **is already on disk**, just the index registration failed (e.g. path not within allowed root). Content isn't lost, just find it at that path;
- Peer-provided `path` contains `../` or absolute paths → they get sanitized before joining, and there's a final "must land within `pulled/`" check. This prevents malicious peers from writing through your disk — **not** the system fighting you.

---

## 5.6 Saved ≠ Shared Out (Important)

Pulled files default to only being in **your own content store** — they are **not** automatically shared out.

This is the same principle as in Chapter 2, just reversed:

```text
Other's node ──save──> My content store ──check Chapter 4──> My share list ──> Others
```

In other words, the act of "I saved someone else's content" **does not make you a redistribution source**, unless you go check it again yourself. To make it visible externally, just do Chapter 4 again.

---

## 5.7 What Is the Panel's "Ingest from Network"

The panel also has a URL input row (optional save name alongside, empty infers from URL) + **Ingest from Network** button: it tells **the node you're connected to** to fetch an HTTP(S) URL and ingest it — different from "pull from another Peerdrive node." The source is a regular website, not a Peerdrive node.

- Use case: feed a public file from the web directly into the node (e.g. an ISO, a dataset);
- Failures are usually not network problems but **the node protecting itself**: SSRF blocking (internal addresses), exceeding size limits, URL 404 / requires login. Check the error text in the task.

---

## 5.8 Common Failures & Their Meanings

| Symptom | Cause / What to Do |
|---|---|
| `404 collection not shared by peer` | Peer's current list doesn't have it: unshared / private and you're not on friend list / wrong ID |
| `502` | Can't get peer's list: peer offline, PSK didn't pass, signaling unreachable (debug per Chapter 1 §1.6) |
| `open stream: ...` | Connected but the peer won't give it right now: disconnected, or this is private and you're not a friend |
| `hash mismatch: got <12-digit>` | Peer's file changed / transfer corrupted; delete and retry (corrupt data didn't land on disk) |
| `invalid sha256 hash` | Hash isn't 64-digit hex (probably copy-paste missed the tail) |
| `peer pull not enabled` (503) | This node doesn't have PeerJS enabled (`PEERDRIVE_PEERJS_ENABLE=false`), save feature is completely unavailable |
| Cancel returns `already finished` | Task already ended, no need to cancel |
| Progress always "indeterminate" | Peer didn't declare size (`total: -1`), not stuck — just watch `received` increase |
| Very slow transfer | Single connection is just that fast (no parallel chunking); for batch saves, select multiple at once to maximize 3-way concurrency |
| Clicked "Save" in panel, nothing on node | Normal: panel's Save is browser download. To get it into the node, use the admin console (§5.0) |

---

## 5.9 Chapter Self-Check List

1. Check a file on the peer's detail page to save → transfer page shows `running`, then `done`;
2. That file is visible under `<PEERDRIVE_DOWNLOAD_DIR>/pulled/`;
3. Save the same file again → task immediately `skipped` + `done` (dedup works);
4. Can find it in "My Drive" (registered in the index);
5. It is **not** in your own share list (§5.6);
6. Save a file the peer didn't share but you have the hash for (Chapter 3 unlisted) → it saves;
7. Save content the peer has as `private` and you're not on friend list → fails (Chapter 3);
8. Click cancel during transfer → becomes `cancelled`, `.part` temp file deleted, no half-file left (`.part` may only remain if the node process was force-killed — delete manually).

---

> Design and implementation details: `doc/NETDISK.md` §12 (sharing) and `back/internal/service/peerpull.go` (pull tasks: concurrency gate, sha256 verification, path sanitization, cancel).
