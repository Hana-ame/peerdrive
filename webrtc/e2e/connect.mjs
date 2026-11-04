// e2e：从 webrtc.log 抓 PEER ID，用 Chromium 打开 demo，走完整链路：
//   1. 浏览器用原生 RTCPeerConnection 发 offer（信令借 PeerJS）
//   2. webrtc-node 应答，DataChannel 打开
//   3. 发 control ping → 等到 pong（证明双向）
//   4. 发 metadata → 等到 ack
//   5. 发 8 MiB 随机块 + sha256 → 等到 check 回执
//
// 环境变量：
//   PEER_LOG   默认 webrtc.log
//   DEMO_URL   默认 http://127.0.0.1:8792/index.html
//   PW_CHANNEL 本地调试可设 msedge
import fs from "node:fs";
import { chromium } from "playwright";

const logPath = process.env.PEER_LOG || "webrtc.log";
const demoURL = process.env.DEMO_URL || "http://127.0.0.1:8792/index.html";
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

function log(...a) { console.log("[e2e]", ...a); }
function fail(msg) {
  console.error("E2E FAIL: " + msg);
  process.exit(1);
}

// ---- 1. 抓 go 侧 id ----

let goID = "";
const deadline = Date.now() + 90_000;
while (Date.now() < deadline) {
  if (fs.existsSync(logPath)) {
    const text = fs.readFileSync(logPath, "utf8");
    const marker = text.indexOf("PEER ID");
    if (marker >= 0) {
      const m = text.slice(marker).match(UUID);
      if (m) { goID = m[0]; break; }
    }
  }
  await new Promise((r) => setTimeout(r, 1000));
}
if (!goID) { fail(`90s 内没能从 ${logPath} 读到 PEER ID`); }
log("go peer id:", goID);

// ---- 2. 打开页面 ----

const launchOpts = process.env.PW_CHANNEL ? { channel: process.env.PW_CHANNEL } : {};
const browser = await chromium.launch(launchOpts);
const page = await browser.newPage();
page.on("console", (msg) => {
  if (msg.type() === "error") { log("[page console]", msg.text()); }
});
page.on("pageerror", (err) => log("[page error]", err.message));

await page.goto(demoURL, { waitUntil: "domcontentloaded" });

await page.waitForFunction(
  () => /^[0-9a-f-]{36}$/i.test((document.getElementById("myId") || {}).textContent || ""),
  null,
  { timeout: 90_000 },
);
log("demo 页面已注册到信令");

// ---- 3. 建连 ----

await page.fill("#peerId", goID);
await page.click("#connectBtn");

let dcOpen = false;
for (let i = 0; i < 45; i++) {
  const chip = (await page.textContent("#chipDC")) || "";
  if (chip.includes("open")) { dcOpen = true; break; }
  if ((i + 1) % 15 === 0) { log(`  等 DataChannel… ${i + 1}s · ${chip.trim()}`); }
  await page.waitForTimeout(1000);
}
if (!dcOpen) {
  const state = await page.textContent("#logbox");
  console.error(state);
  await browser.close();
  fail("45s 内 DataChannel 没打开");
}
log("DataChannel 已打开");

// ---- 4. control ping → pong ----

await page.click("#pingBtn");
let rtt = "";
for (let i = 0; i < 20; i++) {
  rtt = (await page.textContent("#chipRTT")) || "";
  if (rtt.includes("rtt")) { break; }
  await page.waitForTimeout(500);
}
if (!rtt.includes("rtt")) {
  const state = await page.textContent("#logbox");
  console.error(state);
  await browser.close();
  fail("没收到 pong");
}
log("control 往返 OK ·", rtt.trim());

// ---- 5. metadata → ack ----

const beforeMeta = await page.locator("#msgs .msg.in").count();
await page.click("#metaBtn");
let sawAck = false;
for (let i = 0; i < 20; i++) {
  const kinds = await page.locator("#msgs .msg.in .k").allTextContents();
  if (kinds.includes("ack")) { sawAck = true; break; }
  await page.waitForTimeout(500);
}
if (!sawAck) { await browser.close(); fail("metadata 没有收到 ack"); }
log("metadata → ack OK");

// ---- 6. 8 MiB 大块 + sha256 ----

await page.click("#blobBtn");
let sawCheckBack = false;
let digest = "";
for (let i = 0; i < 60; i++) {
  const last = await page.locator("#msgs .msg.in .body").last().textContent().catch(() => "");
  const kinds = await page.locator("#msgs .msg.in .k").allTextContents();
  const checkIdx = kinds.lastIndexOf("check");
  if (checkIdx >= 0) {
    // 找最后一条 check 帧的正文
    const bodies = await page.locator("#msgs .msg.in .body").allTextContents();
    digest = bodies[checkIdx] || "";
    if (digest.toLowerCase().includes("ack from webrtc-node")) { sawCheckBack = true; break; }
  }
  if ((i + 1) % 20 === 0) { log(`  等大块回执… ${i + 1}s · ${last.slice(0, 60)}`); }
  await page.waitForTimeout(1000);
}

// 8 MiB 走 TURN 可能慢，给足时间但别无限等
if (!sawCheckBack) {
  const logText = await page.textContent("#logbox");
  console.error(logText);
  await browser.close();
  fail("8 MiB 传输没有收到 check 回执");
}
log("8 MiB + sha256 → 回执 OK");

// ---- 7. 页面上不该有文本帧错误 ----

const logText = await page.textContent("#logbox");
if (logText.includes("意外的文本帧")) {
  await browser.close();
  fail("出现了文本帧（应全为二进制）");
}

const summary = {
  goID,
  dcOpen,
  rtt: rtt.trim(),
  sawAck,
  sawCheckBack,
  checkBody: digest.slice(0, 120),
};
await browser.close();
console.log(JSON.stringify(summary, null, 2));
console.log("E2E OK");
