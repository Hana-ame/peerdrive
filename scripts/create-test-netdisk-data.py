#!/usr/bin/env python3
import os
import sys
import math
import struct
import zlib
import hashlib
import json

def make_png(width, height, color_func):
    """Generate an uncompressed or zlib-compressed PNG byte stream without external dependencies."""
    raw = bytearray()
    for y in range(height):
        raw.append(0)  # filter type 0 (None)
        for x in range(width):
            r, g, b = color_func(x, y, width, height)
            raw.extend((max(0, min(255, int(r))),
                        max(0, min(255, int(g))),
                        max(0, min(255, int(b)))))

    compressed = zlib.compress(bytes(raw), 6)

    def chunk(tag, data):
        return struct.pack('>I', len(data)) + tag + data + struct.pack('>I', zlib.crc32(tag + data))

    ihdr = struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr) + chunk(b'IDAT', compressed) + chunk(b'IEND', b'')

def cyber_neon(x, y, w, h):
    u = x / w
    v = y / h
    grid = 1.0 if (x % 24 < 2 or y % 24 < 2) else 0.0
    r = int(180 * (1 - v) + 120 * grid)
    g = int(30 * u + 200 * grid * (1 - v))
    b = int(240 * v + 180 * (1 - u))
    return r, g, b

def emerald_lake(x, y, w, h):
    u = x / w
    v = y / h
    wave = math.sin(u * 12 + v * 4) * 20
    r = int(10 + 20 * v)
    g = int(140 + 70 * (1 - v) + wave)
    b = int(110 + 90 * v)
    return r, g, b

def sunset_ocean(x, y, w, h):
    u = x / w
    v = y / h
    sun_dist = math.sqrt((u - 0.5)**2 + (v - 0.35)**2)
    sun = max(0, 1.0 - sun_dist * 2.5) ** 2
    r = int(240 * (1 - v * 0.4) + sun * 40)
    g = int(90 * (1 - v) + sun * 140)
    b = int(40 * (1 - v) + 160 * v)
    return r, g, b

def galaxy_space(x, y, w, h):
    u = x / w
    v = y / h
    stars = 1.0 if ((x * 37 + y * 53) % 97 == 0) else 0.0
    r = int(60 * u + 90 * (1 - v) + stars * 200)
    g = int(20 + 40 * v + stars * 200)
    b = int(140 + 100 * (1 - u * v) + stars * 200)
    return r, g, b

def main():
    out_dir = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else "./run/shared")
    os.makedirs(out_dir, exist_ok=True)
    print(f"[create-test-netdisk-data] Generating files into: {out_dir}")

    images = [
        ("cyberpunk_neon_city.png", cyber_neon, 320, 240),
        ("emerald_forest_lake.png", emerald_lake, 320, 240),
        ("sunset_ocean_horizon.png", sunset_ocean, 320, 240),
        ("peerdrive_galaxy_space.png", galaxy_space, 320, 240),
    ]

    for fname, func, w, h in images:
        fpath = os.path.join(out_dir, fname)
        data = make_png(w, h, func)
        with open(fpath, "wb") as f:
            f.write(data)
        h_sha = hashlib.sha256(data).hexdigest()
        print(f"  ✓ Created image: {fname} ({len(data)} bytes, sha={h_sha[:12]}...)")

    docs = [
        ("cloud_storage_whitepaper.md", b"# Peerdrive P2P Storage Architecture\n\nContent-addressed distributed netdisk.\nZero central storage, pure WebRTC DataChannel + SHA-256 CAS.\n"),
        ("benchmark_report_2026.json", json.dumps({"benchmark": "p2p-transfer", "version": "v0.2.0", "concurrency": 16, "status": "pass"}, indent=2).encode('utf-8')),
        ("welcome_guide.txt", b"Welcome to Peerdrive Local Node Netdisk.\nBrowse files, preview thumbnails, and inspect collections.\n"),
    ]

    for fname, content in docs:
        fpath = os.path.join(out_dir, fname)
        with open(fpath, "wb") as f:
            f.write(content)
        h_sha = hashlib.sha256(content).hexdigest()
        print(f"  ✓ Created doc: {fname} ({len(content)} bytes, sha={h_sha[:12]}...)")

    print("[create-test-netdisk-data] Done!")

if __name__ == "__main__":
    main()
