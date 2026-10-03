# Auth Module Docs

Authentication/registration service (regserver) related designs and reviews.
**Current status: design proposal stage, code not yet landed** (except node-side auth
middleware and relay register/heartbeat which are implemented, see table below).

- `API-DESIGN.md` — Registration server endpoints (§1), node-side auth endpoints
  (§2), auth middleware (§3), relay registration (§4), test accounts (§5);
  **§6-§10 are 2026-09-19 added design proposals**: user↔node directory, traffic
  statistics and anti-falsification, registered-user encrypted channels, relay
  positioning in node, integration points with existing code and landing order.
- `USER-ROLES.md` — Permission matrix and frontend role determination logic for the
  four roles (anonymous visitor / authenticated user / anonymous+node /
  authenticated+node).
- `SECURITY-REVIEW.md` — Security audit results.

## Integration with peerdrive main repo

| Implemented | Location |
|---|---|
| Node-side auth middleware (`AuthOptional`/`AuthRequired`, token forwarded to regserver for validation) | `back/internal/...` (`PEERDRIVE_REG_SERVER` config) |
| Relay register / heartbeat / list | See `API-DESIGN.md` §4 |
| Collection `Owner` semantics (`visibility` three-tier owner field) | `doc/REFACTOR.md` §3.16 |

| Not implemented (proposals) | See |
|---|---|
| User ↔ node directory | `API-DESIGN.md` §6 |
| Traffic statistics + anti-falsification (countersign/relay backstop/sample challenge/drift detection) | `API-DESIGN.md` §7 |
| Registered users' application-layer encrypted channel + account identity binding | `API-DESIGN.md` §8 |
| Relay traffic statistics reporting | `API-DESIGN.md` §7 + §9 |
