// 帧通道：把 webrtc/frame.js（仓库唯一实现）绑到一个已打开的 DataChannel 上。
//
// 浏览器与 Go 侧共用同一份编解码规则：kind(1)+stream(4) 头、>64 KiB 自动分片、
// 分片按 stream 独立重组。这里只做「通道 ↔ 帧」的适配，不实现协议本身。
const P = globalThis.PDFrame;
if (!P) {
  throw new Error("frame.js 未加载：应通过 /assets/frame.js 引入 webrtc/frame.js");
}

export const KIND = P.KIND;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

function toBytes(data) {
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
  return null;
}

// openFrames(dc, onFrame) → 发送/接收句柄。
// onFrame 收到的是完整帧（分片已重组），顺序按 reach 到达顺序，不保证跨 stream 有序。
export function openFrames(dc, onFrame) {
  const asm = P.newAssembler();
  const state = { open: true };
  dc.binaryType = "arraybuffer";

  dc.addEventListener("message", (ev) => {
    const bytes = toBytes(ev.data);
    if (!bytes) return;
    const frame = P.decode(asm, bytes);
    if (frame) onFrame(frame);
  });
  dc.addEventListener("close", () => {
    state.open = false;
  });

  function sendRaw(kind, stream, bytes) {
    if (!state.open) return false;
    try {
      for (const part of P.encode(kind, stream, bytes)) dc.send(part);
      return true;
    } catch (err) {
      state.open = false;
      return false;
    }
  }

  return {
    get open() {
      return state.open;
    },
    sendJSON(kind, stream, obj) {
      return sendRaw(kind, stream, encoder.encode(JSON.stringify(obj)));
    },
    sendBytes(kind, stream, bytes) {
      return sendRaw(kind, stream, bytes);
    },
    // 文本帧（hello/control/metadata/check/ack/error）的 JSON 解码。
    json(frame) {
      return JSON.parse(decoder.decode(frame.data));
    },
    text(frame) {
      return decoder.decode(frame.data);
    },
  };
}