# Chapter 3: Share Levels — public / unlisted / private

> Continues from Chapter 2: files are already in the node (visible in content store).
> This chapter solves "who to show shared things to."
> **No compilation needed throughout**: admin panel uses buttons, command line uses `curl` against local HTTP.

---

## 3.0 First Remember One Rule: Listed ≠ Given

Last chapter (how to check content) solved "share **what**", this chapter solves "**to whom**". These two are orthogonal,
so there are three levels total:

| Level | Appears in Shared Manifest | Who Can Download | Typical Use |
|---|---|---|---|
| `public` (public) | ✅ Yes | Anyone connected | Want people who join your node to see it at a glance, save with one click |
| `unlisted` (not listed) | ❌ No | Anyone connected (knowing hash is enough) | Share a specific file with someone, don't want strangers browsing the manifest |
| `private` (private) | ❌ No (except friends) | **Only you and friends** | Private content between self-hosted nodes |

One-sentence mnemonic: **public = listed and given; unlisted = not listed but given; private = only for people you know.**

> "Shared manifest" means the list returned when a peer sends a `share` frame asking "what do you have"
> (`GET /peerjs/nodes/<peer-id>/shares`). **Not listed** doesn't mean you can't get it —
> as long as you know the file's hash, send `req` and you can retrieve it; this is the basic capability of content addressing.

---

## 3.1 public: Listed, Anyone Can Download

Default level. You checked "share" in the admin panel without changing the level, that's this one:

```bash
curl -s -X POST http://127.0.0.1:3001/peerjs/share/files \
  -H 'Content-Type: application/json' \
  -d '{"hashes":["<64-char-hex>"],"shared":true,"level":"public"}'
```

Effect: any peer connected to your node can see this file in their "Node Details / Market",
click to save it to their own node.

---

## 3.2 unlisted: Not Listed, But Hash Can Retrieve ("Link Sharing")

```bash
curl -s -X POST http://127.0.0.1:3001/peerjs/share/files \
  -H 'Content-Type: application/json' \
  -d '{"hashes":["<64-char-hex>"],"shared":true,"level":"unlisted"}'
```

Effect: it **won't** appear in the `share` manifest (strangers can't find it by browsing), but if you tell someone the hash, they can retrieve it.

**What's the difference between unlisted and "never shared at all"?** The difference is it's **explicitly declared by you**:

- Visible in the admin panel (can audit, modify, count);
- If you later turn on "unlisted content also denied", it won't be accidentally affected;
- Whereas "unchecked files" just happen to be retrievable (existing behavior of content addressing), not in any manifest.

> Want undecleared content also unretrieveable? Current version doesn't have this switch: content-addressed retrieval (know hash
> can retrieve) is the default capability of this system; PSK is the real access gate (see §3.3).

### Give the Hash to Others: Panel's "Link" Button

"Can download by hash" only works if there's an action to "give out the hash" — otherwise you can only verbally
pass around a 64-character hex string. The public panel's shared manifest has a "Link" button on each row; clicking generates:

```text
panel.html?node=<node-id>&hash=<64-char-hex>&auto=1
```

The other party opens it: panel automatically connects to that node, and lists this content **above** the shared manifest
("this content comes from a sharing link"), click "Retrieve" to get it.

This entry **intentionally doesn't rely on the shared manifest**: unlisted by definition isn't in the manifest, if you only look in the manifest,
the link recipient sees "this node has no shared content" and leaves, while the content was always retrievable.

Links **don't contain** the pre-shared key — keys can't be forwarded or left in someone else's history; nodes needing gates,
link recipients need to ask you for the key separately.

### Collections Can Also Get a Link as a Package

Above is about **single files**. Collections work the same way — their manifest (entry list) itself is content-addressed JSON,
`hash` is the sha256 of that JSON, so "retrieve by hash" works for collections too: what you retrieve isn't content, it's the **entry list**.

The "Link" button on the collection title row gives this kind of link (the "Link" on each row within the manifest gives a
**single entry**, don't confuse them). When the other party opens it:

- Panel recognizes this as a collection (if it's in the manifest, recognized directly; if not in the manifest — e.g., unlisted — it
  fetches the manifest back to check, if there's an `entries` array it's a collection);
- Entries listed one by one **above** the manifest, each can "Save / Preview / Link" individually;
- Panel can only save to browser individually (browsers block triggering multiple downloads at once). **To save the whole collection into your
  own node, use the admin panel's "Save Entire Collection"** ([Chapter 5](05-save-from-other-nodes.md)).

This fills the one legitimate exit for unlisted collections: not listed, but people who know the hash can get it.
Saving into **your own node** as a package also works — admin panel "Save Entire Collection" falls back to fetching the manifest by hash when it can't find it in the manifest (details in [Chapter 5](05-save-from-other-nodes.md) §5.2).

⚠️ **private collections don't even give strangers the manifest** — the manifest contains all entries' paths and
hashes, leaking it equals leaking the directory. So strangers opening a private collection link see "retrieval failed",
not the entry list.

---

## 3.3 private: Only for You and Friends

```bash
# 1) Set this file as private
curl -s -X POST http://127.0.0.1:3001/peerjs/share/files \
  -H 'Content-Type: application/json' \
  -d '{"hashes":["<64-char-hex>"],"shared":true,"level":"private"}'

# 2) Fill in the friend list (node IDs, comma-separated)
curl -s -X PUT http://127.0.0.1:3001/peerjs/share \
  -H 'Content-Type: application/json' \
  -d '{"friends":["pd-mom","pd-laptop"]}'
```

Who can retrieve:

| Requestor | Can Retrieve Private? |
|---|---|
| Yourself (admin panel / panel direct to local channel) | ✅ Always, no need to add friend |
| Nodes in friend list | ✅ Yes, **and can see it in the shared manifest** (otherwise permission given but no directory) |
| Others | ❌ Can't retrieve, returns `err: private` |

**Where to see Node ID**: admin panel top/node page shows your node's peer id; when a peer connects to you,
your node log also has `peer=<id>`.

### If the Other Party Uses the Public Panel: ID is in the Panel's Top-Right

The friend list matches the **peer ID the other party self-reports when connecting**. Panels previously generated a random one each time they opened,
filling it in the list was pointless (a different person the next day). Now the "My Node ID" in the panel's top-right is fixed
(starts with `pd-panel-`, stored in this browser), have the other person click "Copy" and send to you, fill into the list:

```bash
curl -s -X PUT http://127.0.0.1:3001/peerjs/share \
  -H 'Content-Type: application/json' \
  -d '{"friends":["pd-panel-8f3k2a9q"]}'
```

After they click "Switch", the old id becomes invalid, need to re-add. (Opening two panel tabs in the same browser
might also collide on id; the panel auto-switches and reconnects, the UI explains why.)

### ⚠️ Friend List Is Only Reliable When PSK Is Set

Peer ID is **self-reported by the peer**: signaling doesn't verify identity, a stranger can change their id to your friend's
name and connect. So:

- **To truly block strangers, set `PEERDRIVE_PSK` first** (access gate: can't even connect without the key);
- The friend list is a second-level filter among "people already inside", **not** identity verification;
- If you need "only specific accounts can see" strong identity, you need to wait for the account system (see `doc/NETDISK.md` §12.6).

---

## 3.4 When Multiple Sources Hit the Same File: Take the Most Relaxed

Three sources (single file check / entire directory / collection) might simultaneously hit the same file, in which case the **most relaxed**
level wins:

| Directory | Single File Check | Effective Level |
|---|---|---|
| unlisted | public | **public** (listed) |
| public | private | **public** (directory is more relaxed) |
| unlisted | (not checked) | unlisted |

Why take the most relaxed rather than strictest: taking the strictest would silently invalidate "I intentionally relaxed this file" —
what you'd see in the UI is "I changed it, but after refresh it's back to normal."

Conversely, **if you explicitly pick a level in the dropdown, that's an override**: changing an already-public file to private
takes effect immediately, no need to uncheck first then recheck.

---

## 3.5 How to Verify

**Check current state and three-level values**:

```bash
curl -s http://127.0.0.1:3001/peerjs/share | python -m json.tool
```

Excerpt (note each row now has `level`):

```json
{
  "enable": true,
  "levels": ["public", "unlisted", "private"],
  "friends": ["pd-mom"],
  "files": [
    {"hash":"<64-char-hex>","name":"a.txt","size":1234,"shared":true,"by_dir":false,"level":"private"}
  ],
  "selected": [{"id":"<64-char-hex>","level":"private"}]
}
```

**Chapter Self-Check List**:

1. Files set to `unlisted` do **not appear** in the peer's `shares` manifest;
2. But they can still be retrieved by hash (panel "Retrieve Verify" or another node `POST /p2p/pull`, see Chapter 5);
3. After setting to `private`, nodes not in the friend list retrieving it → returns `private`;
4. After adding yourself to the friend list, the same node can retrieve, **and** can see it in `shares`;
5. Restart node → levels and friend list both persist (stored in `share_scope.json`);
6. Collection title row's "Link" shared out → the other party sees a **collection** (entries listed), not a single file;
   after setting a collection to unlisted, the link still works; after setting to private, strangers can't even see the entry list.

---

## 3.6 Boundaries and Several "Errors" That Are Actually Protection

| Phenomenon | Cause / What to Do |
|---|---|
| Level misspelled as `"pubilc"` returns 400 | Illegal level **rejects the entire batch**, won't silently fallback to public (that would equal publicly exposing content meant to be restricted). Only three levels can be used |
| Friend ID contains spaces → 400 | Copy errors are invisible to the eye (`abc ` vs `abc`), reject all and prompt |
| Added friend, but they still can't retrieve | ① Their connecting ID doesn't match what you filled (case-insensitive, but no extra spaces); ② They didn't actually pass PSK |
| Set to private, can't even retrieve yourself | Won't happen: admin panel / panel direct to local channel always counts as "yourself". P2P peers use the friend list |
| Collection itself is "specified permissions only / self only" | Treated as **private**: not listed, only friends. Because access list is an account list, current version has no identity verification |

> For judgment and wiring details: `doc/NETDISK.md` §12.6.
> Next chapter (Chapter 4) covers how to check content row by row, and how check vs level works in the admin panel.
