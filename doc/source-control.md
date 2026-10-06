# Source Control Plane Design (Draft)

> 2026-08-19 · Documentation first. Goal: in addition to the existing "read-plane Source interface",
> add a **control plane** for each source, so users can actively manage/write/download files.
> Currently only documenting the design, no code changes.

## 1. Why a Control Plane is Needed

The current `Source` interface is **read-only**:

- `Open`: streaming read by hash
- `Fetch`: bulk read by hash
- `Info`: query metadata
- `Available`: whether available

But actual usage also requires "write/manage" capabilities, such as:

- Adding existing local files to the local source
- Directly writing files to the local source
- Actively downloading a torrent / magnet via BT
- Actively pin / unpin / download a CID via IPFS

These don't belong to "read by hash", but to the **source control plane**.

## 2. Control Plane Principles

- **Keep the read plane stable**: the existing `Source` interface is not broken.
- **Control plane as optional capability**: not every source must implement it.
- **Unified entry point**: `SourceControl`/router handles it centrally, avoiding each source building its own HTTP.
- **Once control plane write is complete**: the file enters the content-addressed system, and is subsequently retrieved via the read-plane `Open/Fetch`.

## 3. Control Plane Interface Draft

```go
// Control is an optional control capability for a source.
// Implementations can implement only the subset they support; unsupported ones return ErrUnsupported.
type Control interface {
    // Local: add an existing local file to the source (compute hash, register in index)
    AddLocalFile(path string) (*FileMeta, error)

    // Local: directly write a file, compute hash and register after writing
    WriteFile(name string, r io.Reader) (*FileMeta, error)

    // BT: download a torrent / magnet to local storage and register the result file
    DownloadTorrent(location string, opts TorrentOptions) (*TorrentTask, error)

    // BT: query task status / cancel task / list
    TorrentStatus(taskID string) (*TorrentTask, error)
    CancelTorrent(taskID string) error
    ListTorrents() ([]TorrentTask, error)

    // IPFS: actively pin / unpin / download by CID
    PinCID(cid string) (*FileMeta, error)
    UnpinCID(cid string) error
    ListPins() ([]PinInfo, error)
}
```

> Specific method names can be refined later; this draft first expresses "what control capabilities each source has".

## 4. Current Control Plane Status per Source

| Source | Control Plane Capabilities | Existing Reusable Code |
|---|---|---|
| Local | `AddLocalFile`, `WriteFile` | `transport.FileIndexService.Create`, `UploadSession` |
| Peer | Not yet defined | N/A |
| URL | Not yet defined | N/A |
| IPFS | `PinCID`, `UnpinCID`, `ListPins`; serve IPFS protocol independently; control manages its behavior | `internal/controller/p2p.go` already has pin endpoints, `internal/provider/ipfs.go`, `front/src/pages/IPFS.jsx` |
| BT | `DownloadTorrent`, status/cancel/list | `back/p2p_bt` already has DHT fetch; torrent active download pending integration |

## 5. IPFS Source Specialities: Serving IPFS Protocol Itself

IPFS is not just a passive source that "pulls files from public gateways"; it can also **independently serve the IPFS protocol** on this node:

- Expose `/ipfs/:cid` or an IPFS-compatible API, letting other clients/nodes access CIDs on this node directly.
- Internally fetches through Bitsswap / gateway / DHT.
- Whether to enable it, which paths to expose, whether to allow pinning, which gateways are available — all controlled by the Control plane.

Therefore, IPFS control plane design is divided into two categories:

| Category | Capabilities |
|---|---|
| Content Management | `PinCID`, `UnpinCID`, `ListPins`, download by CID |
| Protocol Service Control | Enable/disable IPFS serve, configure gateway list, switch Bitsswap/HTTP mode, expose `/ipfs/:cid` route, view service status |

The corresponding control interface can be extended to:

```go
type IPFSControl interface {
    Control // Generic: AddLocalFile/WriteFile etc. if applicable

    // Content Management
    PinCID(cid string) (*FileMeta, error)
    UnpinCID(cid string) error
    ListPins() ([]PinInfo, error)

    // Protocol Service Control
    EnableIPFSServe(enable bool) error
    IPFSServeStatus() (*IPFSServeStatus, error)
    SetGateways(gateways []string) error
    SetBitswapEnabled(enabled bool) error
}
```

## 6. Recommended Landing Points

- Create new `internal/source/control.go`: define the `Control` interface and models like `TorrentOptions`.
- `LocalSource` implements `AddLocalFile` / `WriteFile`, reusing existing file-index/upload logic.
- BT control plane first wraps `back/p2p_bt`, task status stored in `repository` or in-memory table.
- IPFS control plane can reuse existing controller pin logic, later consolidated into unified `SourceControl`.
- Management entry via router/admin, e.g.:
  - `POST /sources/local/add`
  - `POST /sources/local/upload`
  - `POST /sources/bt/download`
  - `GET /sources/bt/tasks`
  - `POST /sources/ipfs/pin`
  - `POST /sources/ipfs/serve/enable`
  - `GET /sources/ipfs/serve/status`

## 7. BT / IPFS as Optional DLL/Plugin (Architecture Preference)

User preference: BT and IPFS should both be optional external modules (on Windows they can be called DLL, **EXE is also acceptable**),
**the module is not included when not needed**, and core peerdrive still works.
The criterion is not "must be DLL or EXE", but: **as long as it can be controlled and used as a Source**.

### Why This is Reasonable

- BT/IPFS involve heavy dependencies, external network protocols, open-source libraries — not everyone needs them.
- As optional modules:
  - The main program doesn't force BT/IPFS dependencies.
  - As long as the corresponding DLL/plugin is not installed, the corresponding source/control shows "unavailable".
  - Deploy the corresponding DLL/plugin only when needed, without affecting main program upgrades.

### Optional Module Approaches in Go

| Approach | Description | Best For |
|---|---|---|
| **Standalone Process/Service** (recommended first choice) | BT/IPFS each as standalone EXE/local service, main program calls via HTTP/gRPC; not deployed when not needed | Most convenient cross-platform, no CGO/DLL loading needed, EXE acceptable |
| **c-shared DLL** | Use cgo to compile BT/IPFS into Windows DLL / Linux .so, dynamically loaded by main program | If a single "DLL file" form is required |
| **Go plugin** | Official Go plugin (`.so`) | Linux only, Windows not supported |
| **build tags optional compilation** | `//go:build bt && ipfs`, if tag not satisfied the corresponding code is not compiled | Decided at build time, not dynamically loaded at runtime |
| **Standalone go.mod** | Like the current `back/p2p_bt`, as a standalone repo/module, main program replaces as needed | Already has a similar structure |

> Given the project runs on Windows, both DLL and EXE are acceptable; recommend **standalone EXE/local service** first,
> exposing "control + use as Source for reading" capabilities through a stable local interface (HTTP/gRPC/JSON).
> If it's just "not included when not needed", standalone processes or build tags are simpler and more reliable.

External modules meet the following conditions to be qualified, whether DLL or EXE:

```text
1. Controllable by the main program: load/unload, enable/disable, pin/download/task management, etc.
2. Usable as a Source: the main program can retrieve file content through it by hash/CID/magnet, etc.
3. When not installed, the main program still works normally, with the corresponding capability marked "unavailable".
```


### Reserved Interface

Add "capability probing" to the Control plane, so the main program knows which external modules are available:

```go
type SourceControl interface {
    // Detect whether external modules are loaded/available
    CapabilityStatus() map[string]CapabilityStatus
}
```

Each optional DLL/module exposes the same set of local interfaces:

- Registered on load: `local` / `peer` / `url` are core, always present.
- Optional modules: when `ipfs` / `bt` is not loaded, `Available=false`, and related control entry points return "module not installed" directly.

### Open-Source Library References (for later selection)

- IPFS / Bitswap:
  - `boxo` (IPFS underlying library, bitswap / gateway)
  - `kubo` / `go-ipfs` RPC or HTTP API (as a standalone process)
- BT / DHT:
  - `github.com/anacrolix/torrent`
  - `github.com/anacrolix/dht/v2`
  - The existing `back/p2p_bt` is already a standalone go.mod and can continue as the BT module foundation

> This stage only records the direction, without binding to specific libraries; selection will be made based on license/size/stability during actual integration.
