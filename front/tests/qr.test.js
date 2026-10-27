import { describe, it, expect } from 'vitest';
import { createQRMatrix, generateQRSVGPath } from '../src/utils/qr';

describe('QR Code Generator (Serverless Peer Exchange)', () => {
  it('generates a valid QR matrix for short peer ID', () => {
    const matrix = createQRMatrix('pd-test-123456');
    expect(matrix).toBeDefined();
    expect(matrix.length).toBeGreaterThanOrEqual(21);
    expect(matrix[0].length).toBe(matrix.length);

    // Verify finder patterns exist at top-left, top-right, bottom-left
    expect(matrix[0][0]).toBe(1);
    expect(matrix[0][matrix.length - 1]).toBe(1);
    expect(matrix[matrix.length - 1][0]).toBe(1);
  });

  it('generates a valid QR matrix for peerdrive:// connection string', () => {
    const token = 'peerdrive://peer/pd-node-alpha-99?host=127.0.0.1&port=9000&psk=secret123';
    const matrix = createQRMatrix(token);
    expect(matrix).toBeDefined();
    expect(matrix.length).toBeGreaterThan(21);

    const { d, size } = generateQRSVGPath(matrix);
    expect(d).toContain('M');
    expect(size).toBe(matrix.length);
  });
});
