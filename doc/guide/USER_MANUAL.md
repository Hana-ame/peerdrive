# Peerdrive User Manual (v2)

## Interface Overview

Peerdrive provides a web-based interface for managing content-addressed file collections.

- **Top navigation bar**: Contains user login/logout, current username setting, and P2P node online status.
- **Main panel**:
    - **Plaza**: Browse and search public collections.
    - **Explorer**: Manage a specific user's specific collection. Includes file list, upload, version commit, and rollback.

---

## Basic Concepts

### 1. Content Addressing (CAS)
Peerdrive doesn't locate files by filename, but by the file's **SHA256 hash**. This means identical files are stored only once regardless of their names.

### 2. Anonymous Collection
A collection is essentially a `path -> hash` mapping list.
- An **anonymous collection** is an immutable JSON file, which is itself stored in the system and has a unique hash.
- Any collection's "snapshot" is an anonymous collection.

### 3. Registered User Collection
Registered users can own multiple named collections.
- Each user collection points to the latest anonymous collection snapshot through a **pointer (current_hash)**.
- This allows collections to be version-managed, snapshotted, rolled back, and quickly forked like Git.

### 4. P2P Auto-Fallback
When downloading a file, the system tries the following order:
`local storage` $\rightarrow$ `remote replica` $\rightarrow$ `P2P network (Bitswap)`
If the file isn't available locally, the system automatically searches the P2P network for other nodes holding that hash and downloads from them.

---

## User Guide

### Authentication and Login
1. Enter username and password in the top navigation bar.
2. Click **Login**. After logging in, you gain permission to perform write operations such as uploading, creating collections, and committing versions.
3. Click **Logout** to clear the session.

### Search Collections
Use the search box on the Plaza page to find public resources of interest by username or collection name.

### Manage Collections
1. **Enter Collection**: Enter `/:username/:collection_name` in the URL or navigate from search results.
2. **Upload File**: Click **+ Upload File**. The file will be hashed and added to the current collection's "workspace".
3. **Commit Version (Commit)**:
    - Enter a description in the commit message box.
    - Click **Commit**. The system generates an immutable anonymous snapshot of the current workspace and updates the collection's current pointer.
4. **Rollback Version (Rollback)**: Select a previous version from the version history list to rollback; the workspace will be restored to that snapshot state.

### Collaboration Operations
- **Fork**: Copy another user's collection completely to your own account.
- **Merge**: Merge another collection's changes into the current collection, supporting different conflict resolution strategies (keep local / accept remote).

---

## FAQ

**Q: Why can I download my uploaded file on other devices?**
A: Because files are stored by content hash. As long as the other party has a reference to that hash (in a collection), and at least one node on the network (including your own node) holds a copy of the file, it can be downloaded.

**Q: What's the difference between Commit and Upload?**
A: Upload only stores the physical file in the system and records it in the current "workspace". Commit freezes all workspace states into a permanent snapshot. Changes without Commit may not be visible to others in certain sync scenarios.

**Q: What does P2P status "Online" mean?**
A: It means your node has successfully started the libp2p protocol, can discover other peer nodes, and participate in file distribution.
