# Cloudflare Pages Build Notes (peerdrive Frontend)

Production deployment depends on: CF dashboard Pages project `peerdrive` → Settings:
- Production branch: `refactor`
- Build configurations: Root `front` / Build `npm ci && npm run build` / Output `dist`
- Build watch paths (Build triggers → Include paths): `*` (or `front/*`)

Production builds are triggered only when pushing to `refactor` and hitting include paths; empty commits (no file changes) will be skipped by watch.
