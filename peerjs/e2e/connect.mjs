// e2e：从 peer.log 抓 PEER ID，用 Chromium 打开 demo，连上去发一条字符串，断言收到回显。
//
// 环境变量：
//   PEER_LOG   默认 peer.log
//   DEMO_URL   默认 http://127.0.0.1:8791/index.html
//   PW_CHANNEL 本地调试可设 msedge，用系统 Edge 而不下载 Chromium
import fs from "node:fs";
import { chromium } from "playwright";

const logPath = process.env.PEER_LOG || "peer.log";
const demoURL = process.env.DEMO_URL || "http://127.0.0.1:8791/index.html";
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

function fail(msg) {
  console.error("E2E FAIL: " + msg);
  process.exit(1);
}

if (!fs.existsSync(logPath)) {
  console.log(`${logPath} 还不存在，等 peer 启动…`);
}

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
if (!goID) {
  fail(`90s 内没能从 ${logPath} 读到 PEER ID`);
}
console.log("Go peer id:", goID);

const launchOpts = process.env.PW_CHANNEL ? { channel: process.env.PW_CHANNEL } : {};
const browser = await chromium.launch(launchOpts);
const page = await browser.newPage();
page.on("console", (msg) => {
  if (msg.type() === "error") { console.log("[page console]", msg.text()); }
});
page.on("pageerror", (err) => console.log("[page error]", err.message));

await page.goto(demoURL, { waitUntil: "domcontentloaded" });

// 等页面自己注册完成
await page.waitForFunction(
  () => /^[0-9a-f-]{36}$/i.test((document.getElementById("myId") || {}).textContent || ""),
  null,
  { timeout: 90_000 },
);
console.log("demo 页面已注册");

await page.fill("#peerId", goID);
await page.click("#connectBtn");

const payload = "e2e-" + Date.now();
let sent = false;
for (let i = 0; i < 60; i++) {
  await page.fill("#msgInput", payload);
  await page.click("#sendBtn");
  if ((await page.locator("#msgs .msg.out").count()) > 0) {
    sent = true;
    console.log(`已发出（${i + 1}s）: ${payload}`);
    break;
  }
  if ((i + 1) % 15 === 0) {
    const state = await page.textContent("#connText");
    console.log(`  等通道… ${i + 1}s · ${state}`);
  }
  await page.waitForTimeout(1000);
}
if (!sent) {
  await browser.close();
  fail("60s 内没能发出，DataChannel 未就绪");
}

let received = "";
for (let i = 0; i < 30; i++) {
  if ((await page.locator("#msgs .msg.in .body").count()) > 0) {
    received = await page.locator("#msgs .msg.in .body").last().textContent();
    break;
  }
  await page.waitForTimeout(1000);
}

await browser.close();

const ok = received.includes(payload);
console.log(JSON.stringify({ goID, payload, sent, received, ok }, null, 2));
if (!ok) {
  fail("没有收到回显");
}
console.log("E2E OK");
