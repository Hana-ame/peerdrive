// 页面编排：只在「阶段/状态分发」这一层，具体协议在 signaling / frames / downloader。
//
// 流程：PeerJS 注册 → 第 1 条通道（信令）→ 第 2 条 DataChannel → 取 collection
//      → 选一个文件拿 sha256 → 分块并行下载 → 逐块 + 整文件 sha256 校验。
import { createPeer, waitPeerOpen, connectSignaling, openDataChannel } from "./signaling.js";
import { KIND, openFrames } from "./frames.js";
import { Downloader } from "./downloader.js";

const $ = (id) => document.getElementById(id);

const ui = {
  peerId: $("peerId"), connectBtn: $("connectBtn"), myId: $("myId"),
  chipSignal: $("chipSignal"), chipPC: $("chipPC"), chipData: $("chipData"), chipColl: $("chipColl"),
  collHead: $("collHead"), fileTable: $("fileTable"), fileList: $("fileList"),
  dlCard: $("dlCard"), dlName: $("dlName"), dlSha: $("dlSha"), dlBar: $("dlBar"),
  dlBytes: $("dlBytes"), dlChunks: $("dlChunks"), dlState: $("dlState"), dlSpeed: $("dlSpeed"),
  dlVerify: $("dlVerify"), threads: $("threads"),
  pauseBtn: $("pauseBtn"), resumeBtn: $("resumeBtn"), reconnectBtn: $("reconnectBtn"),
  log: $("log"),
};

// 给 Playwright / 人工排查一个稳定的断言面，不参与协议。
const E2E = (window.__PEERDRIVE_E2E = { phase: "init", myId: "", collection: null, download: null });

let peer = null;
let conn = null; // 第 1 条通道：只搬信令
let link = null; // 第 2 条通道：{ pc, dc, connectionId }
let frames = null; // 帧通道句柄
let downloader = null;
let currentFile = null;
let collection = null;
let collectionStream = 0;
let streamSeq = 1;

function log(level, text) {
  const line = document.createElement("div");
  if (level) line.className = level;
  line.textContent = `[${new Date().toLocaleTimeString()}] ${text}`;
  ui.log.appendChild(line);
  ui.log.scrollTop = ui.log.scrollHeight;
  E2E.lastLog = text;
}

function setChip(el, state, text) {
  el.className = "chip" + (state ? " " + state : "");
  el.textContent = text;
}

function setPhase(phase) {
  E2E.phase = phase;
}

function fmtBytes(n) {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KiB";
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(2) + " MiB";
  return (n / 1024 / 1024 / 1024).toFixed(2) + " GiB";
}

function fmtSpeed(bps) {
  return bps > 0 ? fmtBytes(bps) + "/s" : "";
}

// ---- 收帧分发：collection 请求与下载 stream 各走各的处理器 ----

function onFrame(f) {
  if (downloader && f.stream >= 1000) {
    downloader.onFrame(f);
    return;
  }
  if (f.stream === collectionStream) {
    if (f.kind === KIND.METADATA) {
      collection = frames.json(f);
      renderCollection(collection);
    } else if (f.kind === KIND.ERROR) {
      log("e", "collection 失败 · " + frames.json(f).message);
      setChip(ui.chipColl, "bad", "collection failed");
    }
    return;
  }
  if (f.kind === KIND.HELLO) {
    log("", "← hello " + frames.text(f));
  } else if (f.kind === KIND.ACK) {
    log("", "← ack");
  }
}

// ---- 连接 ----

async function connect() {
  const goID = ui.peerId.value.trim();
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(goID)) {
    log("e", "PEER ID 形态不对（应形如 8-4-4-4-12）");
    return;
  }
  ui.connectBtn.disabled = true;
  setPhase("peerjs");

  try {
    peer = createPeer(log);
    E2E.myId = await waitPeerOpen(peer);
    ui.myId.textContent = E2E.myId;
    ui.myId.className = "pid on";
    setChip(ui.chipSignal, "on", "peerjs ok");
    log("o", "PeerJS 注册完成 · " + E2E.myId);

    setPhase("signaling");
    conn = await connectSignaling(peer, goID);
    log("o", "第 1 条通道（信令通道）就绪");

    await buildDataChannel();
  } catch (e) {
    log("e", "连接失败 · " + (e?.message || e));
    setPhase("error");
    ui.connectBtn.disabled = false;
  }
}

// buildDataChannel 用第 1 条通道当信令，建第 2 条通道，然后要 collection。
// 断线重连也走这里：只重建第 2 条通道，PeerJS 信令通道不动。
async function buildDataChannel() {
  if (downloader) downloader.channelLost();

  link = await openDataChannel(conn, { log });
  frames = openFrames(link.dc, onFrame);
  link.dc.addEventListener("close", () => {
    setChip(ui.chipData, "bad", "closed");
    if (downloader && (downloader.state === "running" || downloader.state === "paused")) {
      downloader.channelLost();
      renderDownload(downloader.snapshot());
    }
    setPhase("channel-lost");
  });

  setChip(ui.chipPC, "on", "pc ok");
  setChip(ui.chipData, "on", "channel ok");

  if (downloader) {
    downloader.attach(frames);
    log("", "数据通道已重建，从断点继续");
  }
  requestCollection();
}

function requestCollection() {
  collectionStream = streamSeq++;
  frames.sendJSON(KIND.CONTROL, collectionStream, { action: "collection" });
  log("", "→ 请求 collection");
}

// ---- collection 渲染 ----

function renderCollection(coll) {
  setChip(ui.chipColl, "on", "collection ok");
  setPhase("collection-ready");
  ui.collHead.textContent = `${coll.name} · ${coll.count} 个文件 · ${fmtBytes(coll.total_bytes)}`;
  ui.fileTable.classList.remove("hidden");
  ui.fileList.textContent = "";

  coll.files.forEach((file) => {
    const tr = document.createElement("tr");

    const nameTd = document.createElement("td");
    nameTd.className = "mono";
    nameTd.textContent = file.filename;

    const sizeTd = document.createElement("td");
    sizeTd.className = "n";
    sizeTd.textContent = fmtBytes(file.size);

    const shaTd = document.createElement("td");
    const sha = document.createElement("div");
    sha.className = "sha";
    sha.textContent = file.sha256;
    shaTd.appendChild(sha);

    const actTd = document.createElement("td");
    const btn = document.createElement("button");
    btn.type = "button";
    btn.textContent = "下载";
    btn.dataset.sha = file.sha256;
    btn.addEventListener("click", () => startDownload(file));
    actTd.appendChild(btn);

    tr.append(nameTd, sizeTd, shaTd, actTd);
    ui.fileList.appendChild(tr);
  });

  E2E.collection = {
    name: coll.name,
    count: coll.count,
    totalBytes: coll.total_bytes,
    files: coll.files.map((f) => ({ filename: f.filename, size: f.size, sha256: f.sha256 })),
  };
  log("o", `collection 就绪 · ${coll.count} 个文件 · ${fmtBytes(coll.total_bytes)}`);
}

// ---- 下载 ----

function startDownload(file) {
  currentFile = file;
  ui.dlCard.classList.remove("hidden");
  ui.dlName.textContent = `${file.filename} · ${fmtBytes(file.size)}`;
  ui.dlSha.textContent = file.sha256;
  ui.dlBar.className = "bar";
  ui.dlVerify.className = "verify";
  ui.dlVerify.textContent = "下载中，收完逐块校验";

  downloader = new Downloader({
    frames,
    file,
    threads: Number(ui.threads.value) || 4,
    onUpdate: renderDownload,
  });
  ui.pauseBtn.disabled = false;
  ui.resumeBtn.disabled = true;
  setPhase("downloading");
  downloader.start();
  log("", `开始下载 · ${file.filename} · sha256=${file.sha256} · 并行 ${downloader.threads}`);
}

function renderDownload(s) {
  E2E.download = s;
  ui.dlBar.querySelector("i").style.width =
    (s.size ? Math.min(100, (s.doneBytes / s.size) * 100) : 0).toFixed(1) + "%";
  ui.dlBytes.textContent = `${fmtBytes(s.doneBytes)} / ${fmtBytes(s.size)}`;
  ui.dlChunks.textContent = `块 ${s.doneChunks} / ${s.totalChunks}（在飞 ${s.inflight}）`;
  ui.dlState.textContent = s.state;
  ui.dlSpeed.textContent = fmtSpeed(s.bytesPerSec);

  ui.pauseBtn.disabled = s.state !== "running";
  ui.resumeBtn.disabled = s.state !== "paused" && s.state !== "disconnected";

  if (s.state === "done") {
    ui.dlBar.className = "bar done";
    ui.dlVerify.className = "verify ok";
    ui.dlVerify.textContent = `✅ 整文件 sha256 一致 · ${s.sha256}`;
    setPhase("done");
    log("o", `下载完成并校验通过 · ${s.name} · ${fmtBytes(s.size)}`);
  } else if (s.state === "corrupt" || s.state === "error") {
    ui.dlBar.className = "bar bad";
    ui.dlVerify.className = "verify bad";
    ui.dlVerify.textContent = `❌ ${s.error || "校验失败"}`;
    setPhase("failed");
    log("e", s.error || "下载失败");
  } else if (s.state === "disconnected") {
    ui.dlVerify.className = "verify";
    ui.dlVerify.textContent = "通道断开：点「重建数据通道」后从断点继续";
  }
}

// ---- 交互 ----

ui.connectBtn.addEventListener("click", connect);
ui.pauseBtn.addEventListener("click", () => {
  downloader?.pause();
  log("w", "已暂停：不再发新块，在飞的块会收完");
});
ui.resumeBtn.addEventListener("click", () => {
  downloader?.resume();
  log("", "继续：只补未完成的块");
});
ui.reconnectBtn.addEventListener("click", async () => {
  ui.reconnectBtn.disabled = true;
  try {
    setPhase("reconnecting");
    await buildDataChannel();
    if (downloader) {
      renderCollection(collection);
      ui.dlCard.classList.remove("hidden");
      downloader.resume();
    }
  } catch (e) {
    log("e", "重建数据通道失败 · " + (e?.message || e));
    setPhase("channel-lost");
  } finally {
    ui.reconnectBtn.disabled = false;
  }
});
// ---- 仅给 Playwright 用的动作钩子（不参与协议，也不影响 UI 行为） ----
//
// 暂停/重建通道这类动作要靠人点，脚本里用 setTimeout 掐时机不稳定；把同一批
// 按钮点击暴露成函数，e2e 脚本就能「开始下载 → 立刻暂停 → 重建通道 → 继续」
// 地在一次事件循环里排好。
Object.assign(E2E, {
  startDownload(index) {
    if (!collection || !collection.files[index]) throw new Error("collection 里没有第 " + index + " 个文件");
    startDownload(collection.files[index]);
  },
  pause() {
    ui.pauseBtn.click();
  },
  resume() {
    ui.resumeBtn.click();
  },
  rebuild() {
    ui.reconnectBtn.click();
  },
  snapshot() {
    return {
      phase: E2E.phase,
      collection: E2E.collection,
      download: E2E.download,
    };
  },
});
