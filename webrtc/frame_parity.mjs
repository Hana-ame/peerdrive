// frame_parity.mjs —— 被 frame_parity_test.go 拉起来跑的 Node 侧。
//
// 不做断言，只做「照做并汇报」：Go 那边把输入序列化成 JSON 从 argv 传进来，
// 这里用 frame.js 的真实实现算出结果，原样打成 JSON 回 stdout。判定全部
// 留在 Go 侧——测试失败时 t.Errorf 的现场比 node assert 的堆栈可读得多，
// 而且一个测试框架就够了。
//
// 协议：stdin 收 JSON，stdout 回 JSON。用 stdin 而不是 argv 是因为
// 「正好 64 KiB」这类用例填满 65536 字节，作为命令行参数会撞 Windows
// 的命令行长度上限（约 32 KB），本机不会挂、CI 的 windows-latest 会。
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const F = require("./frame.js");

function b64ToBytes(s) {
  return new Uint8Array(Buffer.from(s, "base64"));
}

function bytesToB64(u8) {
  return Buffer.from(u8.buffer, u8.byteOffset, u8.byteLength).toString("base64");
}

function main() {
  // Go 侧结构体带 omitempty 时，空的 encode/decode 会被省掉，
  // 所以这里必须容忍字段缺席（例如只测常量的那一组输入就是 {}）。
  const input = JSON.parse(readFileSync(0, "utf8"));
  const encodeCases = input.encode || [];
  const decodeCases = input.decode || [];
  const out = { cases: [] };

  for (const c of encodeCases) {
    const bytes = b64ToBytes(c.data);
    const frames = F.encode(c.kind, c.stream, bytes);
    out.cases.push({
      name: c.name,
      frames: frames.map((f) => bytesToB64(f)),
    });
  }

  // decode 的每一组用例共用自己的一份 asm（相当于一条独立连接），
  // 保证组装状态不会跨用例泄漏。
  for (const c of decodeCases) {
    const asm = F.newAssembler();
    const got = [];
    for (const raw of c.frames) {
      const f = F.decode(asm, b64ToBytes(raw));
      if (f === null) {
        got.push(null);
        continue;
      }
      got.push({
        kind: f.kind,
        stream: f.stream,
        data: bytesToB64(f.data),
      });
    }
    out.cases.push({ name: c.name, decoded: got });
  }

  out.constants = {
    KIND: F.KIND,
    headerSize: F.HEADER_SIZE,
    maxPayload: F.MAX_PAYLOAD,
    chunkOverhead: F.CHUNK_OVERHEAD,
  };

  process.stdout.write(JSON.stringify(out));
}

main();
