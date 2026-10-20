// transport_and_url.test.mjs — Node-runner contract tests for canonical URLs, 32B binary headers,
// and order-independent transport queue re-exported via front/src/platform/shared.
//
// 发现背景：front/package.json 定义了 `test:node` 脚本指向本文件，用于在不启动 HappyDOM/Vitest
// 的轻量 Node 环境下快速验证 v1.0 三大核心不变量（SSOT 帧协议、自包含唯一 URL、多通道队列）。
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildFileUrl,
  buildCollectionUrl,
  parseDirectTarget,
  TransferProgressHub,
  TransportQueue,
} from '../../src/platform/shared/index.js';
import {
  encodeBinaryHeader32,
  decodeBinaryHeader32,
  FrameDispatcher,
} from '../../../packages/peerdrive-core/src/index.js';

const FILE_SHA = 'c'.repeat(64);
const COLL_SHA = 'd'.repeat(64);

describe('Frontend Node Contract: Canonical Self-Contained URLs (#/f/ & #/c/)', () => {
  it('generates and parses canonical SHA256 file and collection URLs', () => {
    const fileUrl = buildFileUrl(FILE_SHA, {
      baseUrl: 'https://peerdrive.pages.dev',
      peerId: 'node-1',
      filename: 'report.pdf',
      mode: 'spa',
    });
    const parsedFile = parseDirectTarget(fileUrl);
    assert.equal(parsedFile.kind, 'file');
    assert.equal(parsedFile.hash, FILE_SHA);
    assert.equal(parsedFile.peerId, 'node-1');
    assert.equal(parsedFile.filename, 'report.pdf');

    const collUrl = buildCollectionUrl(COLL_SHA, {
      baseUrl: 'https://peerdrive.pages.dev',
      peerId: 'node-2',
      name: 'Album',
      mode: 'spa',
    });
    const parsedColl = parseDirectTarget(collUrl);
    assert.equal(parsedColl.kind, 'collection');
    assert.equal(parsedColl.hash, COLL_SHA);
    assert.equal(parsedColl.peerId, 'node-2');
  });
});

describe('Frontend Node Contract: Order-Independent 32B Binary Frames & Queue', () => {
  it('routes interleaved PD02 binary frames accurately across concurrent requests', () => {
    const dispatcher = new FrameDispatcher();
    const gotA = [];
    const gotB = [];
    dispatcher.register('req-a', { onDataChunk: (c) => gotA.push(...c) });
    dispatcher.register('req-b', { onDataChunk: (c) => gotB.push(...c) });

    dispatcher.handleIncoming(encodeBinaryHeader32('req-b', 0, new Uint8Array([9, 8])));
    dispatcher.handleIncoming(encodeBinaryHeader32('req-a', 0, new Uint8Array([1, 2, 3])));
    assert.deepEqual(gotA, [1, 2, 3]);
    assert.deepEqual(gotB, [9, 8]);
  });

  it('tracks live transfer progress and serializes single-lane WS uploads', async () => {
    const hub = new TransferProgressHub();
    const queue = new TransportQueue({ progressHub: hub, wsConcurrency: 1 });
    const seq = [];

    await Promise.all([
      queue.runTransfer({ kind: 'upload', transport: 'ws', name: 'a.bin', total: 10 }, async ({ reportProgress }) => {
        seq.push('a-start');
        reportProgress(10, 10);
        seq.push('a-end');
      }),
      queue.runTransfer({ kind: 'upload', transport: 'ws', name: 'b.bin', total: 10 }, async ({ reportProgress }) => {
        seq.push('b-start');
        reportProgress(10, 10);
        seq.push('b-end');
      }),
    ]);

    assert.deepEqual(seq, ['a-start', 'a-end', 'b-start', 'b-end']);
    assert.equal(hub.getSnapshot().totalCompleted, 2);
  });
});
