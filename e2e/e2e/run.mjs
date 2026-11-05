// e2e：浏览器 ↔ ./peerdrive 全链路。
//
// 验证顺序：
//   1. PeerJS 注册 → 第 1 条通道（只搬信令）
//   2. 用第 1 条通道交换 SDP → 第 2 条 WebRTC DataChannel
//   3. 第 2 条通道取 collection → 从集合里取一个文件的 sha256
//   4. 按 sha256 分块并行下载 → 暂停 → 重建通道 → 从断点续传
//   5. 逐块校验 + 整文件 sha256 与 collection 里的值比对
//
// 环境变量：
//   PEER_LOG  默认 /tmp/pd-e2e.log（节点日志，脚本从里面抓 PEER ID）
//   DEMO_URL  默认 http://127.0.0.1:8791/
//   PW_CHANNEL 本地调试可设 msedge，用系统 Edge 而不下载 Chromium
import fs from "node:fs";
import { chromium } from "playwright";

const logPath = process.env.PEER_LOG || "/tmp/pd-e2e.log";
const demoURL = process.env.DEMO_URL || "http://127.0.0.1:8791/";
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;
const PAUSE_AFTER_MS = 150;

function log(...args) {
  console.log(...args);
}

function fail(msg) {
  console.error("E2E FAIL: " + msg);
  process.exit(1);
}

function goTail() {
  try {
    return fs.readFileSync(logPath, "utf8").slice(-3000);
  } catch {
    return "(读不到节点日志)";
  }
}

// ---- 1. 从节点日志抓 PEER ID ----

let goID = "";
const deadline = Date.now() + 120_000;
while (Date.now() < deadline) {
  if (fs.existsSync(logPath)) {
    const text = fs.readFileSync(logPath, "utf8");
    const marker = text.indexOf("PEER ID");
    if (marker >= 0) {
      const m = text.slice(marker).match(UUID);
      if (m) {
        goID = m[0];
        break;
      }
    }
  }
  await new Promise((r) => setTimeout(r, 1000));
}
if (!goID) fail(`120s 内没能从 ${logPath} 读到 PEER ID\n--- 节点日志 ---\n` + goTail());
log("1/5 节点 PEER ID:", goID);

// ---- 2. 打开页面并连上 ----

const launchOpts = process.env.PW_CHANNEL ? { channel: process.env.PW_CHANNEL } : {};
const browser = await chromium.launch(launchOpts);
const page = await browser.newPage();
page.on("console", (msg) => {
  const t = msg.type();
  if (t === "error" || t === "warning") log(`[page console:${t}]`, msg.text());
});
page.on("pageerror", (err) => log("[page error]", err.message));

await page.goto(demoURL, { waitUntil: "domcontentloaded" });
await page.waitForFunction(
  () => /^[0-9a-f-]{36}$/i.test((document.getElementById("myId") || {}).textContent || ""),
  null,
  { timeout: 90_000 },
);
log("2/5 页面已注册到 PeerJS");

async function state() {
  return page.evaluate(() => (window.__PEERDRIVE_E2E ? window.__PEERDRIVE_E2E.snapshot() : null));
}

await page.fill("#peerId", goID);
await page.click("#connectBtn");

try {
  await page.waitForFunction(
    () => window.__PEERDRIVE_E2E && window.__PEERDRIVE_E2E.phase === "collection-ready",
    null,
    { timeout: 90_000 },
  );
} catch {
  fail("90s 内没拿到 collection\n" + JSON.stringify(await state()) + "\n--- 节点日志 ---\n" + goTail());
}
const snap = await state();
const coll = snap.collection;
if (!coll || !coll.files || coll.files.length === 0) {
  fail("collection 为空\n" + JSON.stringify(snap));
}
log(`3/5 第 2 条通道就绪 · collection ${coll.count} 个文件`);

// ---- 3. 选文件（取最大的那个）并下载 ----

const index = coll.files.reduce((best, f, i, all) => (f.size > all[best].size ? i : best), 0);
const file = coll.files[index];
log(`  选中 ${file.filename} · ${file.size} B · sha256=${file.sha256}`);

await page.evaluate((i) => window.__PEERDRIVE_E2E.startDownload(i), index);
await page.waitForFunction(
  () => window.__PEERDRIVE_E2E.download && window.__PEERDRIVE_E2E.download.state === "done",
  null,
  { timeout: 120_000 },
);
const first = await state();
if (!first.download.verified || first.download.sha256 !== file.sha256) {
  fail("首次下载校验失败\n" + JSON.stringify(first.download) + "\n--- 节点日志 ---\n" + goTail());
}
log(`4/5 首次下载完成并校验通过 · ${first.download.doneChunks}/${first.download.totalChunks} 块`);

// ---- 4. 断点续传：暂停 → 重建第 2 条通道 → 继续 ----

// 参数必须显式传进页面：page.evaluate 的函数是在页面里重新绑定的，
// 外面模块作用域里的常量（PAUSE_AFTER_MS）在那边不存在。
await page.evaluate(
  ([i, pauseMs]) => {
    window.__PEERDRIVE_E2E.startDownload(i);
    setTimeout(() => window.__PEERDRIVE_E2E.pause(), pauseMs);
  },
  [index, PAUSE_AFTER_MS],
);

try {
  await page.waitForFunction(
    () =>
      window.__PEERDRIVE_E2E.download &&
      (window.__PEERDRIVE_E2E.download.state === "paused" ||
        window.__PEERDRIVE_E2E.download.state === "done"),
    null,
    { timeout: 60_000 },
  );
} catch {
  fail("暂停后 60s 内状态没落到 paused/done\n" + JSON.stringify(await state()));
}
const paused = await state();
if (paused.download.state === "done") {
  // 机器太快，暂停时已经下完：续传路径没法真的走一遍，明确记为跳过而不是假绿。
  log("  警告：暂停时已下载完成，断点续传步骤跳过（文件太小/链路太快）");
} else {
  if (paused.download.doneChunks >= paused.download.totalChunks) {
    fail("paused 状态却已下完所有块\n" + JSON.stringify(paused.download));
  }
  log(`  暂停在 ${paused.download.doneChunks}/${paused.download.totalChunks} 块`);

  await page.evaluate(() => window.__PEERDRIVE_E2E.rebuild());
  await page.waitForFunction(
    () => window.__PEERDRIVE_E2E.download && window.__PEERDRIVE_E2E.download.state === "done",
    null,
    { timeout: 120_000 },
  );
  const resumed = await state();
  if (!resumed.download.verified || resumed.download.sha256 !== file.sha256) {
    fail("续传后校验失败\n" + JSON.stringify(resumed.download) + "\n--- 节点日志 ---\n" + goTail());
  }
  log(`5/5 重建通道后从断点续传完成并校验通过 · ${resumed.download.doneBytes} B`);
}

await browser.close();
log("E2E PASS");