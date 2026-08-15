#!/bin/bash
set -e

# Create a test file and a valid .torrent for it.
# Output: /tmp/peerdrive-bt-test/test.txt (content)
#         /tmp/peerdrive-bt-test/test.torrent (bencoded torrent)

mkdir -p /tmp/peerdrive-bt-test
TEST_FILE="/tmp/peerdrive-bt-test/test.txt"
TORRENT_FILE="/tmp/peerdrive-bt-test/test.torrent"

echo "BT integration test data $(date)" > "$TEST_FILE"

# Use Python to create a proper bencoded .torrent file
python3 "$(dirname "$0")/create_torrent.py" "$TEST_FILE" "$TORRENT_FILE"
