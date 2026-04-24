# Development Log - Peerdrive

This log tracks all architectural and implementation changes to the Peerdrive project to facilitate collaborative multi-agent development.

## 1. Project Initialization & Framework
- **Go Module**: Initialized project as `peerdrive`.
- **Framework**: Integrated `github.com/gin-gonic/gin` for HTTP routing.
- **API Documentation**: Integrated `swaggo/swag` and `gin-swagger`. Added Swagger annotations to controllers.

## 2. Architecture Refactoring
- **Package Structure**:
    - `main.go`: Entry point, initializes global managers and starts server.
    - `router/`: Defines API routes and middleware.
    - `controller/`: Contains request handlers (business logic).
    - `provider/`: Abstracts data sources.
    - `db/`: Handles metadata persistence.

## 3. Content-Addressable Storage System
Implemented a decoupled resource retrieval system where files are identified by SHA256 hashes but stored in varied locations.

### Provider Pattern (`provider/`)
- **`ContentProvider` Interface**: Defined a standard interface for fetching content (`GetContent`).
- **`LocalProvider`**: Implements local filesystem access.
- **`HTTPProvider`**: Implements remote resource fetching via HTTP.
- **`ProviderManager`**: Manages registered providers and routes requests to the appropriate implementation.

### Metadata Layer (`db/`)
- **Database**: Integrated `sqlite3` via `github.com/mattn/go-sqlite3`.
- **Schema**: Created `files` table storing:
    - `hash` (PK): SHA256 identifier.
    - `path`: Actual resource location (file path or URL).
    - `provider_type`: Which provider to use (`local`, `http`).
    - `filename`: Original name for download headers.

## 4. API Implementation
- `GET /ping`: Basic health check.
- `GET /sha256sum/:sha256`: 
    - Validates SHA256 format.
    - Lookups metadata in SQLite.
    - Delegates to the corresponding Provider.
    - Streams data via `c.DataFromReader`.

## 5. Testing & Quality Assurance
- **Unit Tests**: Implemented `controller_test.go` for isolated handler testing.
- **Integration Tests**: 
    - Created `router_test.go` to test the full request-response cycle.
    - Implemented `test.sh` for end-to-end verification (seeds DB $\rightarrow$ starts server $\rightarrow$ curls endpoints).
- **Seeding**: Created `seed_db.go` to automate test environment setup.

## 6. Documentation
- **`DESIGN.md`**: Detailed architecture and API specification.
- **Swagger**: Auto-generated docs available at `/swagger/index.html`.

## 7. Repo-based Download System
Added support for `/repo@username/path` format for file download, enabling sharing via peer networks.

### Database Schema

#### files 表 - /sha256sum/:hash 使用
| hash | filename |
|------|----------|
| TEXT PRIMARY KEY | TEXT |

#### repo_files 表 - /repo@username/path 使用
| repo_name | username | path | hash | provider_type | file_path | filename | created_at |
|----------|---------|------|------|-------------|----------|----------|-----------|
| TEXT NOT NULL | TEXT NOT NULL | TEXT NOT NULL | TEXT REFERENCES files(hash) | TEXT NOT NULL | TEXT NOT NULL | TEXT | TIMESTAMP DEFAULT CURRENT_TIMESTAMP |

PRIMARY KEY (repo_name, username, path, created_at)

### Design Rationale
- **files table**: Pure content identifier for `/sha256sum/:hash` endpoint (existing)
- **repo_files table**: Maps `repo@username/path` to hash, includes provider_type and file_path for peer-to-peer sharing
- Each repo can have its own storage provider (local, http, or peer network)
- History preserved via append-only inserts (multiple rows per path with different timestamps)
