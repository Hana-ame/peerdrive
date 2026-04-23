# Peerdrive Download System Design

## Overview
The system provides a content-addressable download portal. Files are identified by their SHA256 hash, but the actual storage location is decoupled via a metadata layer.

## Architecture

### 1. Content Addressing
- **Identifier**: SHA256 hash of the file content.
- **Lookup**: `Hash` $\rightarrow$ `Metadata (DB)` $\rightarrow$ `Provider` $\rightarrow$ `Data Stream`.

### 2. Metadata Layer (SQLite3)
The database maps a unique hash to its resource location and provider type.
- **Table `files`**:
    - `hash`: Primary Key (SHA256).
    - `path`: The resource path (local file path or remote URL).
    - `provider_type`: The provider to use (`local`, `http`, etc.).
    - `filename`: Original filename for the download header.

### 3. Provider Pattern
A pluggable provider system allows fetching data from various sources:
- **LocalProvider**: Reads files from the local filesystem using the path stored in DB.
- **HTTPProvider**: Fetches data from a remote URL stored in DB.
- **ProviderManager**: Routes the request to the correct provider based on `provider_type`.

## API Specification

### Download Endpoint
- **URL**: `GET /sha256sum/:sha256`
- **Logic**:
    1. Validate SHA256 format.
    2. Query SQLite for metadata.
    3. Delegate fetch to the matched `ContentProvider`.
    4. Stream data using `c.DataFromReader`.

## Test Suite
- **Unit Tests**: Test individual controllers.
- **Integration Tests**: 
    - Validates Local storage flow.
    - Validates Remote HTTP flow.
    - Validates 404 handling for missing hashes.
    - Validates 400 handling for invalid hash formats.
