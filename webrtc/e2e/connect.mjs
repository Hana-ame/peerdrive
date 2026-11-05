// e2e：从 webrtc.log 抓 PEER ID，用 Chromium 打开 demo，走完整链路：
//   1. 浏览器用 peer.connect() 建连（SDP/ICE 全交给 PeerJS 的信令层）
//   2. webrtc-node 通过 RegisterTransport 接管 OFFER，自己建 PeerConnection
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

function readTail(path, n) {
  try {
    const t = fs.readFileSync(path, "utf8");
    return t.length > n ? t.slice(-n) : t;
  } catch (e) {
    return "(读不到 " + path + ": " + e.message + ")";
  }
}
function fail(msg) {
  console.error("E2E FAIL: " + msg);
  // CI 上 step 日志要沿嵌套链接点好几层才看得到，
  // 这里把失败点写进 GITHUB_STEP_SUMMARY，run 详情页直接能读。
  const s = process.env.GITHUB_STEP_SUMMARY;
  if (s) {
    try {
      fs.appendFileSync(s, "### E2E FAIL\n\n```\n" + msg + "\n```\n");
    } catch (e) { /* summary 写不进去不影响主流程 */ }
  }
  process.exit(1);
}

// ---- 1. 抓 go 侧 id ----

// 抓不到 id 也要继续往下判：webrtc.log 是同一进程的 stdout+stderr，
// 而 log.Printf 的标记只证明「进程起来了」。真挂在这里，八成是信令没
// 连上——那种情况下页面侧同样建不了连，把日志头部带出去比一个 LAUNCH
// FAILED 有用。
let goID = "";
let logTextAtBoot = "";
const deadline = Date.now() + 90_000;
while (Date.now() < deadline) {
  if (fs.existsSync(logPath)) {
    logTextAtBoot = fs.readFileSync(logPath, "utf8");
    const marker = logTextAtBoot.indexOf("PEER ID");
    if (marker >= 0) {
      const m = logTextAtBoot.slice(marker).match(UUID);
      if (m) { goID = m[0]; break; }
    }
  }
  await new Promise((r) => setTimeout(r, 1000));
}
if (!goID) {
  fail(`90s 内没能从 ${logPath} 读到 PEER ID\n--- go 侧日志 ---\n` + readTail(logPath, 4000));
}
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
let lastChip = "";
for (let i = 0; i < 45; i++) {
  lastChip = ((await page.textContent("#chipDC")) || "").trim();
  if (lastChip.includes("open")) { dcOpen = true; break; }
  if ((i + 1) % 15 === 0) { log(`  等 DataChannel… ${i + 1}s · ${lastChip}`); }
  await page.waitForTimeout(1000);
}
if (!dcOpen) {
  const state = (await page.textContent("#logbox")) || "";
  const signalChip = ((await page.textContent("#chipSignal")) || "").trim();
  console.error(state);
  await browser.close();
  fail(
    "45s 内 DataChannel 没打开\n" +
    "chipSignal=" + signalChip + " chipDC=" + lastChip + "\n" +
    "--- 页面日志 ---\n" + state.slice(-3000) +
    "\n--- go 侧日志 ---\n" + readTail(logPath, 4000),
  );
}
// 探针已达成，撤掉：第 20 轮用它确认了「DataChannel 能打开」，
// 现在让流程继续往下跑到 ping。
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
  const state = (await page.textContent("#logbox")) || "";
  const inKinds = await page.locator("#msgs .msg.in .k").allTextContents();
  const outKinds = await page.locator("#msgs .msg.out .k").allTextContents();
  console.error(state);
  await browser.close();
  fail(
    "没收到 pong\n" +
    "in=" + JSON.stringify(inKinds) + " out=" + JSON.stringify(outKinds) + "\n" +
    "--- 页面日志 ---\n" + state.slice(-3000) +
    "\n--- go 侧日志 ---\n" + readTail(logPath, 6000),
  );
}
log("control 往返 OK ·", rtt.trim());

// ---- 5. metadata → ack ----

// RTT 之前成功只证明对端回了 pong。这里等的 ack 是另一条独立路径
// （Go 侧 KindMetadata 分支），而且 ack 与 pong 共用同一个帧协议，
// 所以失败时要把入站帧全列出来，看是没发、没解、还是没画上去。
const beforeMeta = await page.locator("#msgs .msg.in").count();
await page.click("#metaBtn");
let sawAck = false;
for (let i = 0; i < 20; i++) {
  const kinds = await page.locator("#msgs .msg.in .k").allTextContents();
  if (kinds.includes("ack")) { sawAck = true; break; }
  await page.waitForTimeout(500);
}
if (!sawAck) {
  const state = (await page.textContent("#logbox")) || "";
  const inKinds = await page.locator("#msgs .msg.in .k").allTextContents();
  const outKinds = await page.locator("#msgs .msg.out .k").allTextContents();
  console.error(state);
  await browser.close();
  fail(
    "metadata 没有收到 ack\n" +
    "in=" + JSON.stringify(inKinds) + " out=" + JSON.stringify(outKinds) +
    " (beforeMeta=" + beforeMeta + ")\n" +
    "--- 页面日志 ---\n" + state.slice(-3000) +
    "\n--- go 侧日志 ---\n" + readTail(logPath, 4000),
  );
}
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
if (process.env.GITHUB_STEP_SUMMARY) {
  fs.appendFileSync(
    process.env.GITHUB_STEP_SUMMARY,
    "### E2E OK\n\n```json\n" + JSON.stringify(summary, null, 2) + "\n```\n",
  );
}
