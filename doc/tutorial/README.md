# Peerdrive Tutorial

> For people who "want to get this project running and use it," progresses chapter by chapter; each chapter can be independently completed and self-verified.
> **Main-line chapters require no compilation** — just download the released binary.
> Detailed design and pitfall records are not here — consult `doc/NETDISK.md` · `doc/REFACTOR.md` · `doc/NODE.md` when needed.

## Main Line

| Chapter | Contents |
|---|---|
| [Chapter 1: How to Run and Connect Your Own Node](01-run-and-connect.md) | Download release → start node (signaling defaults to public, no deployment needed) → connect via panel / admin console → troubleshooting if it won't connect |
| [Chapter 2: Adding Local Files to the Node for Viewing](02-add-local-files.md) | Content store ≠ share list · panel ingest / `POST /files/upload` / register directory · three viewing perspectives · why "ingest succeeded but not in the list" |
| [Chapter 3: Share Levels — public / unlisted / private](03-share-levels.md) | Listed and granted / not listed but granted / only for self and friends · how to fill in the friend list · the difference between "not shared" and "unlisted" · why PSK is required |
| [Chapter 4: Freely Choose What to Share](04-choose-what-to-share.md) | Admin console row-by-row checks (with level dropdown) / command line three endpoints · single file · directory · collection three granularities · selections persist after restart · how to verify peers can actually see it |
| [Chapter 5: Cross-Node Save](05-save-from-other-nodes.md) | Panel "Save" vs admin console "Save Selected" difference · save entire collection · transfer page progress and cancel · save location and sha256 verification · saved ≠ shared out · common failure meanings |

## Appendix (Can Be Skipped, Not a Chapter Number)

| Appendix | Contents |
|---|---|
| [Appendix A: Build from Source](appendix-build-from-source.md) | When building from source is necessary · build node/signaling · one-shot e2e script · build panel and admin console · run tests · build your own release package |

> The appendix is for "modifying code / running unreleased commits / running tests / building your own package" — if you just want to get the project running, finishing Chapter 1 is enough. Chapter numbers are reserved for the main line; appendices don't take slots.

> ⚠️ The repo also contains `doc/archive/TUTORIAL.md`, written in 2026-05, covering the libp2p stack at that time
> (`p2p-test`, multiaddr, bootstrap peer). **That content has been invalidated along with the libp2p stack removal**
> (fully deleted 2026-08-16, see `doc/archive/LEGACY.md`). The current interconnect layer is PeerJS + WebRTC,
> use this directory as the source of truth.
