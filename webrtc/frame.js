// 帧编解码 —— 浏览器侧的唯一实现。
//
// 这个文件会被三方使用：
//   1. index.html 通过 <script src="frame.js"> 引入，走 window.PDFrame
//   2. frame_parity_test.go 用 node 起一个临时 HTTP 服务把它喂给
//      Chromium，再拿页面的编解码结果跟 Go 的 EncodeFrame/ParseChunk 对拍
//   3. 想调试协议的人可以直接 node -e 'require("./frame.js")'
//
// 之所以抽出来而不是留在 index.html 里：对拍测试要从 HTML 里抠函数，
// 抠法一旦跟改版错位，测试就变成永远通过（或者永远报错）。独立文件
// 至少让「协议实现」有一个明确的、可被 require 的落点。
//
// 协议必须与 frame.go 逐字节一致：
//
//	单帧   kind(1) + stream(4, BE) + data
//	分片帧 kind(1) + stream(4, BE) + flag=1(1) + seq(4, BE) + total(4, BE) + piece
//
// 单帧与分片帧共享同一个 5 字节头，靠正文首字节区分：那是 0x01 就按
// 分片解析。这个歧义是协议自身的（单帧没有独立标志位），所以上层所有
// 文本 payload 都是 JSON（以 { 或 [ 开头），二进制 payload 走 KindData
// 且由 Go 侧保证 ≥ 65 字节才会切片——碰不到首字节恰好为 1 的情形。
(function (root, factory) {
  if (typeof module === "object" && module.exports) {
    module.exports = factory();
  } else {
    root.PDFrame = factory();
  }
})(typeof self !== "undefined" ? self : this, function () {
  "use strict";

  var KIND = { HELLO: 1, CONTROL: 2, METADATA: 3, DATA: 4, CHECK: 5, ACK: 6, ERROR: 7 };
  var KIND_NAME = { 1: "hello", 2: "control", 3: "metadata", 4: "data", 5: "check", 6: "ack", 7: "error" };

  // 与 frame.go 的两个常量逐字对应。
  var HEADER_SIZE = 5;
  var MAX_PAYLOAD = 64 * 1024;
  var CHUNK_OVERHEAD = 9;

  // asm 是重组缓冲。跟 Go 的 Reassembler 一样，一个连接一份、非并发安全
  // （DataChannel 的 onmessage 是串行的）。
  function newAssembler() {
    return { stream: 0, kind: 0, total: 0, parts: [], got: 0 };
  }

  function resetAsm(asm) {
    asm.stream = 0;
    asm.kind = 0;
    asm.total = 0;
    asm.parts = [];
    asm.got = 0;
  }

  // encode(kind, stream, bytes) -> Uint8Array[]
  // 每个元素都是可直接 dc.send 的完整帧（含 5 字节头）。
  function encode(kind, stream, bytes) {
    var out = [];
    if (bytes.length <= MAX_PAYLOAD) {
      var b = new Uint8Array(HEADER_SIZE + bytes.length);
      b[0] = kind;
      new DataView(b.buffer).setUint32(1, stream, false);
      b.set(bytes, HEADER_SIZE);
      out.push(b);
      return out;
    }
    var chunk = MAX_PAYLOAD - CHUNK_OVERHEAD;
    var total = Math.ceil(bytes.length / chunk);
    for (var seq = 0; seq < total; seq++) {
      var lo = seq * chunk;
      var hi = Math.min(lo + chunk, bytes.length);
      var buf = new Uint8Array(HEADER_SIZE + CHUNK_OVERHEAD + (hi - lo));
      var v = new DataView(buf.buffer);
      buf[0] = kind;
      v.setUint32(1, stream, false);
      buf[5] = 1;
      v.setUint32(6, seq, false);
      v.setUint32(10, total, false);
      buf.set(bytes.subarray(lo, hi), HEADER_SIZE + CHUNK_OVERHEAD);
      out.push(buf);
    }
    return out;
  }

  // decode(asm, buf) -> {kind, stream, data} | null(还差片)
  // 注意 asm 必须由调用方持有——同一连接的多次 decode 要共用一份状态。
  function decode(asm, buf) {
    if (buf.length < HEADER_SIZE) {
      resetAsm(asm);
      return null;
    }
    var v = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
    var kind = buf[0];
    var stream = v.getUint32(1, false);

    // 没有正文，或者正文首字节不是 1：单帧，直接穿透。
    if (buf.length <= HEADER_SIZE || buf[5] !== 1) {
      var body = buf.subarray(HEADER_SIZE);
      resetAsm(asm);
      return { kind: kind, stream: stream, data: body };
    }
    if (buf.length < HEADER_SIZE + CHUNK_OVERHEAD) {
      resetAsm(asm);
      return null;
    }

    var seq = v.getUint32(6, false);
    var total = v.getUint32(10, false);
    var piece = buf.subarray(HEADER_SIZE + CHUNK_OVERHEAD);

    if (total === 0) {
      // Go 侧对 total=0 返回 ErrChunkMismatch。这里同样丢弃，不能进入
      // 等待状态——否则一个畸形帧能让通道永久卡住。
      resetAsm(asm);
      return null;
    }
    if (asm.total === 0 || asm.total !== total || asm.stream !== stream || asm.kind !== kind) {
      resetAsm(asm);
      asm.stream = stream;
      asm.kind = kind;
      asm.total = total;
      asm.parts = new Array(total);
      asm.got = 0;
    }
    if (seq >= total) {
      resetAsm(asm);
      return null;
    }
    if (!asm.parts[seq]) {
      asm.parts[seq] = piece.slice();
      asm.got++;
    }
    if (asm.got !== asm.total) {
      return null;
    }
    var size = 0;
    for (var i = 0; i < asm.total; i++) {
      if (!asm.parts[i]) {
        resetAsm(asm);
        return null;
      }
      size += asm.parts[i].length;
    }
    var all = new Uint8Array(size);
    var off = 0;
    for (var j = 0; j < asm.total; j++) {
      all.set(asm.parts[j], off);
      off += asm.parts[j].length;
    }
    var f = { kind: asm.kind, stream: asm.stream, data: all };
    resetAsm(asm);
    return f;
  }

  // kindName 只用于界面展示，不参与协议。
  function kindName(k) {
    return KIND_NAME[k] !== undefined ? KIND_NAME[k] : "kind(" + k + ")";
  }

  return {
    KIND: KIND,
    KIND_NAME: KIND_NAME,
    HEADER_SIZE: HEADER_SIZE,
    MAX_PAYLOAD: MAX_PAYLOAD,
    CHUNK_OVERHEAD: CHUNK_OVERHEAD,
    newAssembler: newAssembler,
    resetAsm: resetAsm,
    encode: encode,
    decode: decode,
    kindName: kindName,
  };
});
