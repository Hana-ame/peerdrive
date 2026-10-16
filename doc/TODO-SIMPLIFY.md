# Architecture Simplification Todo

## Data Model Cleanup

| # | Item | Status | Risk |
|---|------|--------|------|
| M1 | `AnonEntry` old format | ✅ Removed | Low |
| M2 | `file_meta` + `file_providers` tables | ❌ Not done | Highest |

## API Endpoint Simplification

| # | Item | Status | Risk |
|---|------|--------|------|
| A1 | `/files/upload` → `/collections/` | ✅ Deprecated (after 2026-08-17 admin migration, no longer mandatory) | Medium |
| A2 | `/files/register_local` → `/collections/` | ✅ Deprecated (same as above) | Medium |
| A3 | `/files/register_url` → `/collections/` | ✅ Deprecated (same as above) | Medium |
| A4 | `/files/register_folder` → `/collections/` | ✅ Deprecated (same as above) | Medium |
| A5 | `/actions/*` → `/collections/` | ✅ Merged + 301 redirect | Medium |
| A6 | `/download/` vs `/sha256sum/` | ✅ Distinguished: sha256sum=local, download=multi-protocol | Medium |

> A1-A4 explanation: After 2026-08-17 frontend fully migrated to local WS admin frames (forwarded internally via transport/admin.go),
> frontend no longer directly calls these HTTP endpoints; endpoints retained per legacy compatibility policy (old frontend/curl/external
> scripts + integration tests using HTTP directly), consistent with router.go comments. Merging into `/collections/` has no benefit,
> see REFACTOR.md §3.10.

## Frontend Cleanup

| # | Item | Status | Risk |
|---|------|--------|------|
| F1 | `Explorer.jsx` merge into `AnonExplorer` | ❌ Not done | Medium |
| F2 | `CollectionBuilder.jsx` | ✅ Removed | Low |
| F3 | `AnonCollectionManager.jsx` | ✅ Removed | Low |
| F4 | Navbar search "files" section | ✅ Removed | Low |

## Miscellaneous

| # | Item | Status | Risk |
|---|------|--------|------|
| X1 | package.json residual | ✅ Cleaned up | Low |
