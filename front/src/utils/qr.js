// qr.js — Lightweight, zero-dependency QR code generator (Model 2, Byte mode, Level L/M)
// Generates SVG matrix path for serverless QR peer exchange without MQTT or external services.

const GF256_EXP = new Uint8Array(512);
const GF256_LOG = new Uint8Array(256);

(function initGF() {
  let val = 1;
  for (let i = 0; i < 255; i++) {
    GF256_EXP[i] = val;
    GF256_EXP[i + 255] = val;
    GF256_LOG[val] = i;
    val = (val << 1) ^ (val & 0x80 ? 0x11d : 0);
  }
  GF256_LOG[0] = 0;
})();

function gfMul(x, y) {
  if (x === 0 || y === 0) return 0;
  return GF256_EXP[GF256_LOG[x] + GF256_LOG[y]];
}

function polyMul(p1, p2) {
  const result = new Uint8Array(p1.length + p2.length - 1);
  for (let i = 0; i < p1.length; i++) {
    for (let j = 0; j < p2.length; j++) {
      result[i + j] ^= gfMul(p1[i], p2[j]);
    }
  }
  return result;
}

function getGeneratorPoly(degree) {
  let g = new Uint8Array([1]);
  for (let i = 0; i < degree; i++) {
    g = polyMul(g, new Uint8Array([1, GF256_EXP[i]]));
  }
  return g;
}

function rsEncode(data, ecCount) {
  const gen = getGeneratorPoly(ecCount);
  const msg = new Uint8Array(data.length + ecCount);
  msg.set(data);

  for (let i = 0; i < data.length; i++) {
    const coef = msg[i];
    if (coef !== 0) {
      for (let j = 0; j < gen.length; j++) {
        msg[i + j] ^= gfMul(gen[j], coef);
      }
    }
  }
  return msg.slice(data.length);
}

// Version table: [version, totalDataBytes, ecBytes, modulesPerSide]
const VERSIONS = [
  { ver: 1, cap: 17, ec: 7, size: 21 },
  { ver: 2, cap: 32, ec: 10, size: 25 },
  { ver: 3, cap: 53, ec: 15, size: 29 },
  { ver: 4, cap: 78, ec: 20, size: 33 },
  { ver: 5, cap: 106, ec: 26, size: 37 },
  { ver: 6, cap: 134, ec: 36, size: 41 },
];

export function createQRMatrix(text) {
  const utf8 = new TextEncoder().encode(text);
  let vInfo = VERSIONS.find((v) => utf8.length <= v.cap);
  if (!vInfo) {
    vInfo = VERSIONS[VERSIONS.length - 1];
  }

  const { size, cap, ec } = vInfo;
  const matrix = Array.from({ length: size }, () => new Int8Array(size).fill(-1));

  // Finder patterns
  function addFinder(r, c) {
    for (let i = -1; i <= 7; i++) {
      for (let j = -1; j <= 7; j++) {
        const nr = r + i;
        const nc = c + j;
        if (nr >= 0 && nr < size && nc >= 0 && nc < size) {
          const isBlack =
            (i >= 0 && i <= 6 && (j === 0 || j === 6)) ||
            (j >= 0 && j <= 6 && (i === 0 || i === 6)) ||
            (i >= 2 && i <= 4 && j >= 2 && j <= 4);
          matrix[nr][nc] = isBlack ? 1 : 0;
        }
      }
    }
  }
  addFinder(0, 0);
  addFinder(0, size - 7);
  addFinder(size - 7, 0);

  // Timing patterns
  for (let i = 8; i < size - 8; i++) {
    if (matrix[6][i] === -1) matrix[6][i] = i % 2 === 0 ? 1 : 0;
    if (matrix[i][6] === -1) matrix[i][6] = i % 2 === 0 ? 1 : 0;
  }

  // Dark module
  matrix[size - 8][8] = 1;

  // Reserve format info area
  for (let i = 0; i < 9; i++) {
    if (matrix[8][i] === -1) matrix[8][i] = 0;
    if (matrix[i][8] === -1) matrix[i][8] = 0;
  }
  for (let i = size - 8; i < size; i++) {
    if (matrix[8][i] === -1) matrix[8][i] = 0;
    if (matrix[i][8] === -1) matrix[i][8] = 0;
  }

  // Build bitstream: Mode 8-bit byte (0100) + Count (8 bits) + Data + Terminator
  const bits = [];
  const pushBits = (val, count) => {
    for (let i = count - 1; i >= 0; i--) {
      bits.push((val >> i) & 1);
    }
  };

  pushBits(0b0100, 4); // Byte mode
  pushBits(utf8.length, 8); // Character count
  for (const b of utf8) {
    pushBits(b, 8);
  }
  // Terminator
  for (let i = 0; i < 4 && bits.length < cap * 8; i++) {
    bits.push(0);
  }
  // Byte align
  while (bits.length % 8 !== 0) {
    bits.push(0);
  }
  // Pad bytes
  const padBytes = [0xec, 0x11];
  let padIdx = 0;
  while (bits.length < cap * 8) {
    pushBits(padBytes[padIdx % 2], 8);
    padIdx++;
  }

  // Convert bits to data bytes
  const dataBytes = new Uint8Array(cap);
  for (let i = 0; i < cap; i++) {
    let b = 0;
    for (let j = 0; j < 8; j++) {
      b = (b << 1) | bits[i * 8 + j];
    }
    dataBytes[i] = b;
  }

  // Compute Error Correction
  const ecBytes = rsEncode(dataBytes, ec);
  const totalData = new Uint8Array(cap + ec);
  totalData.set(dataBytes);
  totalData.set(ecBytes, cap);

  // Convert totalData to bit array for placement
  const allBits = [];
  for (const b of totalData) {
    for (let i = 7; i >= 0; i--) {
      allBits.push((b >> i) & 1);
    }
  }

  // Populate data using 2-column zig-zag traversal
  let bitIdx = 0;
  let upwards = true;
  for (let right = size - 1; right > 0; right -= 2) {
    if (right === 6) right--; // skip vertical timing column
    const left = right - 1;
    for (let r = 0; r < size; r++) {
      const row = upwards ? size - 1 - r : r;
      for (const col of [right, left]) {
        if (matrix[row][col] === -1) {
          const bit = bitIdx < allBits.length ? allBits[bitIdx++] : 0;
          // Apply mask 0: (row + col) % 2 === 0
          const mask = (row + col) % 2 === 0 ? 1 : 0;
          matrix[row][col] = bit ^ mask;
        }
      }
    }
    upwards = !upwards;
  }

  // Write format info for Level L, Mask 0 (Format bits: 111011111000100)
  const formatBits = [1, 1, 1, 0, 1, 1, 1, 1, 1, 0, 0, 0, 1, 0, 0];
  // Around top-left
  for (let i = 0; i < 6; i++) matrix[8][i] = formatBits[i];
  matrix[8][7] = formatBits[6];
  matrix[8][8] = formatBits[7];
  matrix[7][8] = formatBits[8];
  for (let i = 0; i < 6; i++) matrix[5 - i][8] = formatBits[9 + i];

  // Around top-right and bottom-left
  for (let i = 0; i < 8; i++) matrix[8][size - 1 - i] = formatBits[i];
  for (let i = 0; i < 7; i++) matrix[size - 7 + i][8] = formatBits[8 + i];

  return matrix;
}

/**
 * generateQRSVGPath: Creates an SVG path data string from boolean QR matrix.
 */
export function generateQRSVGPath(matrix) {
  const size = matrix.length;
  let d = '';
  for (let r = 0; r < size; r++) {
    for (let c = 0; c < size; c++) {
      if (matrix[r][c] === 1) {
        d += `M${c},${r}h1v1h-1z `;
      }
    }
  }
  return { d, size };
}
