# Peerdrive Operation Guide

> Repository: `github.com/Hana-ame/peerdrive`
> Go branch: `feat/stage2-e2e-test` / React branch: `frontend`

---

## 1. Clone the Repository

```bash
git clone git@github.com:Hana-ame/peerdrive
cd peerdrive
```

**Note**: The Go backend and React frontend are **two independent git repositories**, clone them to separate directories with different branches:

```bash
# Go backend
git clone -b feat/stage2-e2e-test git@github.com:Hana-ame/peerdrive peerdrive-go
cd peerdrive-go

# React frontend (separate directory)
git clone -b frontend git@github.com:Hana-ame/peerdrive peerdrive-react
cd peerdrive-react
```

---

## 2. Start the Backend

```bash
cd peerdrive-go
go build -o peerdrive-server ./cmd/server/
PORT=3000 PEERDRIVE_STORAGE=./storage PEERDRIVE_P2P_ENABLE=false ./peerdrive-server
```

See `Listening and serving HTTP on :3000` for successful startup.

Verify: `curl --noproxy '*' http://localhost:3000/ping` returns `pong`

> If Privoxy proxy is present, all curl commands need `--noproxy '*'` or `-x ""`

---

## 3. Run Go Unit Tests

```bash
cd peerdrive-go
go test ./... -count=1
```

Expected output: 5 `ok` results (config / controller / model / repository / service), all 66 tests passing.

---

## 4. Run E2E Tests

Self-contained script that builds + starts server + cleans up:

```bash
cd peerdrive-go
bash test/e2e-all.sh
```

Expected results:
```
PASS: 84  FAIL: 0  WARN: 1  TOTAL: 85
All tests passed
```

The only WARN: empty body creating collection returns 200 (edge behavior difference), no impact.

Script workflow:
1. Creates temporary directory at `/tmp/peerdrive_e2e_all/`
2. `go build` compiles latest version
3. Starts server on port 3999 (P2P disabled)
4. Tests 12 sections with 85 assertions in sequence
5. trap auto-cleanup (kill process + delete temp files + delete peerdrive.db)

---

## 5. Start the Frontend

```bash
cd peerdrive-react
npm install        # first time only
npm run dev
```

Visit `http://localhost:5173`

Page navigation:
- `/` — Plaza: lists collections, search, create new
- `/:user/:coll` — Explorer: upload files, Commit, Merge, version history, local sync
- `/anon/create` — Create anonymous collection
- `/anon` — Anonymous collection browser: browse, Fork, **Commit**

The frontend API address is hardcoded on line 1 of `src/api.js` as `https://wsl-3000.moonchan.xyz`; change to `http://localhost:3000` for local development.

---

## 6. Frontend Build

```bash
cd peerdrive-react
npm run build
npm run preview   # preview build artifacts
```

---

## 7. Run Other Test Scripts (requires server running on port 3000)

```bash
# Terminal 1: start server
cd peerdrive-go
PORT=3000 PEERDRIVE_STORAGE=./storage PEERDRIVE_P2P_ENABLE=false go run ./cmd/server/main.go

# Terminal 2: run tests
bash test/upload.sh
bash test/register.sh
bash test/anon-collection.sh
bash test/test.sh         # full integration test
bash test/p2p.sh          # P2P dual-node test (self-contained)
bash test/relay.sh        # Relay penetration test (self-contained)
```

---

## 8. Environment Variables Quick Reference

| Variable | Default | Description |
|------|--------|------|
| `PORT` | `3000` | HTTP port |
| `PEERDRIVE_STORAGE` | `./storage` | File storage directory |
| `PEERDRIVE_P2P_ENABLE` | `true` | Enable P2P |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | P2P listen address |
| `PEERDRIVE_MDNS_ENABLE` | `true` | mDNS LAN discovery |

DB is fixed to `peerdrive.db` in the working directory.

---

## 9. FAQ

**Q: curl reports Privo proxy error**  
A: Add `--noproxy '*'` or `export no_proxy='*'`

**Q: E2E test reports "Server failed to start"**  
A: Check port 3999 isn't occupied: `fuser -k 3999/tcp`

**Q: Frontend page API reports 404**  
A: Check line 1 of `src/api.js`, verify `API_BASE` points to the correct backend address

**Q: Frontend reports module not found**  
A: Run `npm install` to install dependencies

**Q: Go build fails**  
A: Ensure Go 1.21+ is installed and network can access GitHub (for downloading go mod dependencies)
