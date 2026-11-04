// m/peerjs 静态自检：这个分支没有编译产物，能坏的地方就是 HTML / JS / 契约本身。
// 本地与 CI 都跑同一个脚本：node check.mjs
import fs from "node:fs";
import vm from "node:vm";

let failures = 0;
const ok = (msg) => console.log("ok   " + msg);
const fail = (msg) => { console.error("FAIL " + msg); failures++; };
const read = (p) => fs.readFileSync(p, "utf8");

let html, lib, agents;
for (const p of ["index.html", "peerjs.min.js", "AGENTS.md"]) {
  if (!fs.existsSync(p) || fs.statSync(p).size === 0) { fail(`缺少或空文件: ${p}`); }
}
if (failures) { process.exit(1); }
html = read("index.html");
lib = read("peerjs.min.js");
agents = read("AGENTS.md");

// 1. vendored 库：必须是本地的 1.5.5，不能悄悄变成空壳
if (!/1\.5\.5/.test(lib)) { fail("peerjs.min.js 缺 1.5.5 版本标记"); }
else { ok(`peerjs.min.js 1.5.5 · ${Buffer.byteLength(lib)} B`); }

// 2. 所有内联脚本必须能解析（不执行）
const blocks = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
if (!blocks.length) { fail("index.html 里没有内联脚本"); }
blocks.forEach((b, i) => {
  try { new vm.Script(b, { filename: `inline-${i}.js` }); ok(`内联脚本 #${i} 语法 · ${b.length} B`); }
  catch (e) { fail(`内联脚本 #${i}: ${e.message}`); }
});

// 3. $() 取到的 id 都得真的在 DOM 里
const ids = new Set([...html.matchAll(/id="([^"]+)"/g)].map((m) => m[1]));
const refs = new Set([...html.matchAll(/\$\("([^"]+)"\)/g)].map((m) => m[1]));
const missing = [...refs].filter((r) => !ids.has(r));
if (missing.length) { fail("DOM id 缺失: " + missing.join(", ")); }
else { ok(`DOM 引用 ${refs.size} 个全部命中 · 共 ${ids.size} 个 id`); }

// 4. 页内锚点别指空
const anchors = [...html.matchAll(/href="#([^"]+)"/g)].map((m) => m[1]);
const badAnchors = anchors.filter((a) => !ids.has(a));
if (badAnchors.length) { fail("锚点不存在: " + badAnchors.join(", ")); }
else { ok(`页内锚点 ${anchors.length} 个全部有效`); }

// 5. 模块契约：信令、收发、序列化、双向链接
const contract = [
  "0.peerjs.com",            // 云信令
  "peer.connect",            // 主动连
  "conn.send",               // 发字符串
  "conn.on(",                // 收字符串
  "serialization",           // json 序列化
  "textContent",             // 不拼 innerHTML
  "../hash-to-metadata-impl.html" // 反向链接
];
const absent = contract.filter((s) => !html.includes(s));
if (absent.length) { fail("index.html 契约缺失: " + absent.join(", ")); }
else { ok(`index.html 契约 ${contract.length} 项齐备`); }

// 6. AGENTS.md 只留契约，且必须写明两条信令地址
for (const s of ["0.peerjs.com", "PEERDRIVE_PEERJS_URL", "PEERDRIVE_SERVER_URL", "serialization"]) {
  if (!agents.includes(s)) { fail("AGENTS.md 缺少: " + s); }
}
ok("AGENTS.md 契约与 .env 地址");

if (failures) {
  console.error(`\n${failures} 项失败`);
  process.exit(1);
}
console.log("\nall checks passed");
