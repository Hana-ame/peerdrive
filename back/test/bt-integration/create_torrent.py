#!/usr/bin/env python3
"""Create a bencoded .torrent file from a single file."""

import hashlib
import os
import struct
import sys


def bencode_int(i: int) -> bytes:
    return b"i" + str(i).encode() + b"e"


def bencode_str(s: bytes) -> bytes:
    return str(len(s)).encode() + b":" + s


def bencode_list(items: list[bytes]) -> bytes:
    return b"l" + b"".join(items) + b"e"


def bencode_dict(d: dict[str, bytes]) -> bytes:
    result = b"d"
    for k in sorted(d.keys()):
        result += bencode_str(k.encode()) + d[k]
    return result + b"e"


def main():
    if len(sys.argv) < 3:
        print("Usage: create_torrent.py <input_file> <output_torrent>")
        sys.exit(1)

    src = sys.argv[1]
    dst = sys.argv[2]
    data = open(src, "rb").read()

    # Piece length: 16 KiB for small test files
    piece_length = 16384

    # Build pieces: concatenated 20-byte SHA1 hashes
    pieces = b""
    for offset in range(0, len(data), piece_length):
        chunk = data[offset : offset + piece_length]
        pieces += hashlib.sha1(chunk).digest()

    # Build the info dict (raw bencoded bytes)
    info_raw = bencode_dict(
        {
            "name": bencode_str(b"test.txt"),
            "piece length": bencode_int(piece_length),
            "pieces": bencode_str(pieces),
            "length": bencode_int(len(data)),
        }
    )

    info_hash = hashlib.sha1(info_raw).hexdigest()

    # Build the full torrent dict
    torrent_raw = bencode_dict(
        {
            "announce": bencode_str(b""),
            "created by": bencode_str(b"peerdrive-bt-test"),
            "creation date": bencode_int(int(os.path.getmtime(src))),
            "info": info_raw,
        }
    )

    with open(dst, "wb") as f:
        f.write(torrent_raw)

    num_pieces = (len(data) + piece_length - 1) // piece_length
    print(f"Created {dst}")
    print(f"  File: {src} ({len(data)} bytes)")
    print(f"  InfoHash: {info_hash}")
    print(f"  Piece length: {piece_length}")
    print(f"  Pieces: {num_pieces}")


if __name__ == "__main__":
    main()
