#!/bin/bash
# Peerdrive 测试数据初始化 — 清空数据库并创建测试合集/文件
# 用法: bash back/test/setup_test_data.sh

set -e

BACK_DIR="$(cd "$(dirname "$0")/.." && pwd)"
STORAGE="$BACK_DIR/storage"
DB="$BACK_DIR/peerdrive.db"

hash_var() { echo "$1" | tr '/.-' '_'; }

echo "=== Peerdrive 测试数据初始化 ==="
echo "数据库: $DB"
echo "存储目录: $STORAGE"

# ── 1. 清空数据库 ──────────────────────────────────────
echo ""
echo ">>> 清空数据表..."
sqlite3 "$DB" <<'SQL'
DELETE FROM transfer_tasks;
DELETE FROM file_providers;
DELETE FROM file_meta;
DELETE FROM version_entries;
DELETE FROM collection_versions;
DELETE FROM collection_entries;
DELETE FROM collections;
DELETE FROM users;
SQL
echo "    完成"

# ── 2. 定义测试文件 ─────────────────────────────────────
declare -A FILE_CONTENT
FILE_CONTENT["README.md"]="# Peerdrive Test\n\nThis repository is for testing the Peerdrive P2P file sharing system.\n"
FILE_CONTENT["src/index.js"]="console.log('Hello from Peerdrive');\n\nmodule.exports = { version: '0.0.1' };\n"
FILE_CONTENT["src/utils/helper.js"]="function helper() { return { status: 'ok', timestamp: Date.now() }; }\n\nmodule.exports = { helper };\n"
FILE_CONTENT["docs/guide.md"]="# User Guide\n\n## Getting Started\n1. Install Peerdrive\n2. Run the server with \`go run ./cmd/server/\`\n3. Open the web interface at http://localhost:3000\n\n## Features\n- P2P file sharing via libp2p\n- Content-addressed storage (SHA256)\n- Version control (Git-like commits)\n- BitTorrent DHT integration\n- IPFS compatibility layer\n"
FILE_CONTENT["docs/api/endpoints.md"]="# API Endpoints\n\n## GET /health\nHealth check. Returns {\"status\":\"ok\"}\n\n## POST /files/upload\nUpload a file. Multipart form, field: \`file\`\n\n## GET /sha256/:hash\nDownload a file by its SHA256 hash\n"
FILE_CONTENT["config.json"]="{\n  \"version\": \"1.0.0\",\n  \"debug\": true,\n  \"p2p\": {\n    \"enabled\": true,\n    \"port\": 4001\n  },\n  \"features\": [\"p2p\", \"ipfs\", \"webdav\", \"bt\"]\n}"
FILE_CONTENT["notes/meeting-notes.txt"]="Meeting Notes - 2026-05-03\n==============================\n\nAttendees: Alice, Bob, Charlie\n\nTopics:\n1. Project status review\n2. Sprint planning\n3. Bug triage\n\nAction Items:\n- Alice: Fix the upload pipeline\n- Bob: Add E2E tests\n- Charlie: Review PR #42\n"
FILE_CONTENT["assets/banner.png"]="[simulated PNG binary content for testing]"

# MIME types by filename pattern
mime_of() {
  case "$1" in
    *.md)    echo "text/markdown" ;;
    *.js)    echo "application/javascript" ;;
    *.json)  echo "application/json" ;;
    *.txt)   echo "text/plain" ;;
    *.png)   echo "image/png" ;;
    *)       echo "application/octet-stream" ;;
  esac
}

ALL_PATHS=(
  "README.md"
  "src/index.js"
  "src/utils/helper.js"
  "docs/guide.md"
  "docs/api/endpoints.md"
  "config.json"
  "notes/meeting-notes.txt"
  "assets/banner.png"
)

echo ""
echo ">>> 创建测试文件..."

for path in "${ALL_PATHS[@]}"; do
  content="${FILE_CONTENT[$path]}"
  mime=$(mime_of "$path")

  # Write content to temp file (printf %b interprets \n, \", etc.)
  printf '%b' "$content" > /tmp/peerdrive_test_file.tmp
  hash=$(sha256sum /tmp/peerdrive_test_file.tmp | cut -d' ' -f1)
  size=$(stat -c%s /tmp/peerdrive_test_file.tmp)

  # Write to content-addressed storage: storage/<hash[:2]>/<hash>
  subdir="${hash:0:2}"
  mkdir -p "$STORAGE/$subdir"
  cp /tmp/peerdrive_test_file.tmp "$STORAGE/$subdir/$hash"

  # Insert file_meta
  filename=$(basename "$path")
  sqlite3 "$DB" "INSERT INTO file_meta (hash, size, mime_type, filename, created_at, type) VALUES ('$hash', $size, '$mime', '$filename', datetime('now'), 'blob');"

  # Insert file_providers (local storage)
  sqlite3 "$DB" "INSERT INTO file_providers (hash, provider_type, path) VALUES ('$hash', 'local', '$subdir/$hash');"

  # Extra HTTP provider for some files
  if [[ "$path" == "README.md" ]] || [[ "$path" == "config.json" ]]; then
    sqlite3 "$DB" "INSERT INTO file_providers (hash, provider_type, path) VALUES ('$hash', 'http', 'https://example.com/$hash');"
  fi

  echo "    $path → $hash ($size bytes)"

  # Save hash for collection entry creation
  varname=$(hash_var "$path")
  eval "HASH_${varname}='$hash'"
done

# ── 3. 创建用户 ─────────────────────────────────────────
echo ""
echo ">>> 创建测试用户..."
sqlite3 "$DB" <<'SQL'
INSERT INTO users (username, password_hash, authkey, created_at) VALUES ('testuser', 'sha256$fakehash', 'test-auth-key-12345', datetime('now'));
SQL
echo "    用户: testuser / authkey: test-auth-key-12345"

# ── 4. 创建合集 ─────────────────────────────────────────
echo ""
echo ">>> 创建测试合集..."

collection_entry() {
  local cid="$1" path="$2"
  local vn; vn=$(hash_var "$path")
  local hv; hv="HASH_${vn}"
  local hash="${!hv}"
  sqlite3 "$DB" "INSERT INTO collection_entries (collection_id, path, file_hash) VALUES ($cid, '$path', '$hash');"
}

# 4a. 单文件合集: test-single (只有 README.md)
sqlite3 "$DB" "INSERT INTO collections (username, collection_name, visibility, follow_redirects, tags, created_at) VALUES ('testuser', 'test-single', 'public', 1, 'test,single-file', datetime('now'));"
COLL_SINGLE_ID=$(sqlite3 "$DB" "SELECT id FROM collections WHERE username='testuser' AND collection_name='test-single';")
collection_entry "$COLL_SINGLE_ID" "README.md"
echo "    单文件合集 'test-single': README.md"

# 4b. 多文件合集: test-multi (所有文件)
sqlite3 "$DB" "INSERT INTO collections (username, collection_name, visibility, follow_redirects, tags, created_at) VALUES ('testuser', 'test-multi', 'public', 1, 'test,multi-file,example', datetime('now'));"
COLL_MULTI_ID=$(sqlite3 "$DB" "SELECT id FROM collections WHERE username='testuser' AND collection_name='test-multi';")

for path in "${ALL_PATHS[@]}"; do
  collection_entry "$COLL_MULTI_ID" "$path"
done

COLL_MULTI_ENTRIES=$(sqlite3 "$DB" "SELECT COUNT(*) FROM collection_entries WHERE collection_id=$COLL_MULTI_ID;")
echo "    多文件合集 'test-multi': $COLL_MULTI_ENTRIES 个文件条目"

# ── 5. 创建版本快照 ─────────────────────────────────────
echo ""
echo ">>> 创建版本快照..."

version_entry() {
  local vid="$1" path="$2"
  local vn; vn=$(hash_var "$path")
  local hv; hv="HASH_${vn}"
  local hash="${!hv}"
  sqlite3 "$DB" "INSERT INTO version_entries (version_id, path, file_hash) VALUES ($vid, '$path', '$hash');"
}

# 单文件合集版本
sqlite3 "$DB" "INSERT INTO collection_versions (collection_id, version_number, commit_message, created_at) VALUES ($COLL_SINGLE_ID, 1, 'Initial commit: single file', datetime('now'));"
VER_SINGLE_ID=$(sqlite3 "$DB" "SELECT id FROM collection_versions WHERE collection_id=$COLL_SINGLE_ID AND version_number=1;")
version_entry "$VER_SINGLE_ID" "README.md"
echo "    单文件合集版本 1 ✓"

# 多文件合集版本
sqlite3 "$DB" "INSERT INTO collection_versions (collection_id, version_number, commit_message, created_at) VALUES ($COLL_MULTI_ID, 1, 'Initial commit: full project', datetime('now'));"
VER_MULTI_ID=$(sqlite3 "$DB" "SELECT id FROM collection_versions WHERE collection_id=$COLL_MULTI_ID AND version_number=1;")
for path in "${ALL_PATHS[@]}"; do
  version_entry "$VER_MULTI_ID" "$path"
done
echo "    多文件合集版本 1 ✓"

# 多文件合集版本 2 (部分文件更新)
sqlite3 "$DB" "INSERT INTO collection_versions (collection_id, version_number, commit_message, parent_version_id, created_at) VALUES ($COLL_MULTI_ID, 2, 'Add config and meeting notes', $VER_MULTI_ID, datetime('now'));"
VER_MULTI_ID2=$(sqlite3 "$DB" "SELECT id FROM collection_versions WHERE collection_id=$COLL_MULTI_ID AND version_number=2;")
version_entry "$VER_MULTI_ID2" "README.md"
version_entry "$VER_MULTI_ID2" "src/index.js"
version_entry "$VER_MULTI_ID2" "config.json"
version_entry "$VER_MULTI_ID2" "notes/meeting-notes.txt"
echo "    多文件合集版本 2 (增量更新) ✓"

# ── 6. 输出 Markdown 表格 ──────────────────────────────
echo ""
echo "=== 测试数据统计 ==="
echo ""

echo "**合集**"
echo ""
echo "| 合集名 | 类型 | 文件数 | 可见性 | 标签 |"
echo "|--------|------|--------|--------|------|"
echo "| test-single | 单文件合集 | 1 | public | test, single-file |"
echo "| test-multi  | 多文件合集 | $COLL_MULTI_ENTRIES | public | test, multi-file, example |"

echo ""
echo "**文件清单**"
echo ""
echo "| 路径 | 路径层次 | MIME 类型 | 大小 | SHA256 |"
echo "|------|----------|-----------|------|--------|"

for path in "${ALL_PATHS[@]}"; do
  vn=$(hash_var "$path")
  hv="HASH_${vn}"
  hash="${!hv}"
  subdir="${hash:0:2}"
  size=$(stat -c%s "$STORAGE/$subdir/$hash" 2>/dev/null || echo "0")
  mime=$(mime_of "$path")

  # Count path depth (number of / separators)
  depth=$(echo "$path" | tr -cd '/' | wc -c)
  if [[ "$depth" -eq 0 ]]; then depth_label="根目录";
  elif [[ "$depth" -eq 1 ]]; then depth_label="1 级子目录";
  else depth_label="2 级子目录"; fi

  echo "| \`$path\` | $depth_label | $mime | $size B | \`$hash\` |"
done

echo ""
echo "**存储分布**"
echo ""
echo "| 目录 | 文件数 | 总大小 |"
echo "|------|--------|--------|"
sqlite3 "$DB" "SELECT '| storage/' || substr(hash,1,2) || '/ | ' || COUNT(*) || ' | ' || SUM(size) || ' B |' FROM file_meta GROUP BY substr(hash,1,2);"

echo ""
echo "**数据库统计**"
echo ""
echo "| 表名 | 记录数 |"
echo "|------|--------|"
sqlite3 "$DB" "SELECT '| file_meta | ' || COUNT(*) || ' |' FROM file_meta;"
sqlite3 "$DB" "SELECT '| file_providers | ' || COUNT(*) || ' |' FROM file_providers;"
sqlite3 "$DB" "SELECT '| collections | ' || COUNT(*) || ' |' FROM collections;"
sqlite3 "$DB" "SELECT '| collection_entries | ' || COUNT(*) || ' |' FROM collection_entries;"
sqlite3 "$DB" "SELECT '| collection_versions | ' || COUNT(*) || ' |' FROM collection_versions;"
sqlite3 "$DB" "SELECT '| version_entries | ' || COUNT(*) || ' |' FROM version_entries;"
sqlite3 "$DB" "SELECT '| users | ' || COUNT(*) || ' |' FROM users;"

echo ""
echo "---"
echo "数据库文件: $(stat -c%s "$DB") B"
echo "存储目录: $(du -sh "$STORAGE" 2>/dev/null | cut -f1)"
echo "=== 完成 ==="
