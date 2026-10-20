// security_and_permissions.test.mjs — Node-runner security & permission boundary tests for front/.
//
// 发现背景：验证前端 platform/shared 导出的不可信输入清洗器（防原型污染、路径穿越、Bidi 欺骗）
// 与 WebRTC 只读管理面门禁（evaluateAdminPermission）契约一致。
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  safeJsonParse,
  sanitizeUntrustedText,
  sanitizeFilename,
  sanitizeRelativePath,
  validateSignalOrApiUrl,
  sanitizeCollectionManifest,
  evaluateAdminPermission,
} from '../../src/platform/shared/index.js';

describe('Frontend Node Security & Permission Contracts', () => {
  it('blocks prototype pollution and path traversal in untrusted peer manifests', () => {
    assert.equal(safeJsonParse('{"__proto__":{"polluted":1}}'), null);
    assert.equal(sanitizeFilename('../../etc/shadow'), 'shadow');
    assert.equal(sanitizeRelativePath('../a/../../b/file.png'), 'a/b/file.png');
    assert.equal(sanitizeUntrustedText('safe\u202Etext'), 'safetext');

    const manifest = sanitizeCollectionManifest({
      name: 'Demo\u0000',
      entries: [{ path: '../dir/pic.jpg', name: '../pic.jpg', size: 128, sha: 'A'.repeat(64) }],
    });
    assert.equal(manifest.name, 'Demo');
    assert.equal(manifest.entries[0].path, 'dir/pic.jpg');
    assert.equal(manifest.entries[0].name, 'pic.jpg');
    assert.equal(manifest.entries[0].sha, 'a'.repeat(64));
  });

  it('rejects non-loopback plaintext signal URLs and WebRTC write verbs', () => {
    assert.equal(validateSignalOrApiUrl('https://0.peerjs.com').valid, true);
    assert.equal(validateSignalOrApiUrl('http://localhost:9000').valid, true);
    assert.equal(validateSignalOrApiUrl('http://remote.example.com:9000').valid, false);

    assert.equal(evaluateAdminPermission('ws', 'POST', '/files/upload').allowed, true);
    assert.equal(evaluateAdminPermission('peerjs', 'GET', '/collections').allowed, true);
    assert.equal(evaluateAdminPermission('peerjs', 'POST', '/collections').allowed, false);
  });
});
