// 双通道建立：PeerJS 只搬信令，第二条 RTCPeerConnection 才跑数据。
//
// 为什么不直接用 PeerJS 的数据通道跑帧：PeerJS 帮你把 OFFER/ANSWER/CANDIDATE
// 都做完了，但同时它把通道的序列化、分片、生命周期也一起管了；要控制「帧」
// 就得和它争这层。这里把两件事拆开——PeerJS 通道只当信令线，帧全部跑在
// 自己建的原始 DataChannel 上，两边边界清楚。

const ICE = [
  { urls: "stun:stun.l.google.com:19302" },
  { urls: "turn:0.peerjs.com:3478", username: "peerjs", credential: "peerjsp" },
];

// createPeer 建一个 PeerJS peer（默认 0.peerjs.com）。
export function createPeer(log) {
  const peer = new Peer({
    host: "0.peerjs.com",
    port: 443,
    path: "/",
    secure: true,
    key: "peerjs",
    debug: 1,
  });
  peer.on("error", (e) => log("e", `peerjs 错误 · ${e?.type || ""} ${e?.message || ""}`));
  peer.on("disconnected", () => {
    log("w", "PeerJS 信令断开，1.5s 后重连");
    setTimeout(() => {
      try {
        peer.reconnect();
      } catch (e) {
        log("e", "重连失败: " + e);
      }
    }, 1500);
  });
  return peer;
}

export function waitPeerOpen(peer) {
  return new Promise((resolve, reject) => {
    if (peer.open) return resolve(peer.id);
    peer.once("open", () => resolve(peer.id));
    peer.once("error", reject);
  });
}

// connectSignaling 建第 1 条通道（JSON 字符串），它只用于搬 SDP。
export function connectSignaling(peer, goID, { timeout = 20000 } = {}) {
  return new Promise((resolve, reject) => {
    const conn = peer.connect(goID, { serialization: "json", reliable: true });
    const timer = setTimeout(() => reject(new Error("第 1 条 PeerJS 通道 20s 未打开")), timeout);
    conn.once("open", () => {
      clearTimeout(timer);
      resolve(conn);
    });
    conn.once("error", (e) => {
      clearTimeout(timer);
      reject(e);
    });
  });
}

// openDataChannel 用第 1 条通道做信令，建第 2 条 PeerConnection + 原始 DataChannel。
//
// 浏览器侧是发起方：createOffer → 把 offer 当字符串发出去 → 收 answer/candidate。
// candidate 可能早于 answer 到达（Go 侧 ICE 在 setLocalDescription 后就开始收集），
// 所以先排队，setRemoteDescription 后再补加。
export async function openDataChannel(conn, { log, timeout = 30000 }) {
  const connectionId = "web-" + Math.random().toString(16).slice(2, 10);
  const pc = new RTCPeerConnection({ iceServers: ICE });
  const dc = pc.createDataChannel("peerdrive", { ordered: true });
  dc.binaryType = "arraybuffer";

  let remoteSet = false;
  const earlyCandidates = [];
  let failure = null;

  pc.addEventListener("connectionstatechange", () => {
    log("", `第 2 条 PeerConnection · ${pc.connectionState} / ice ${pc.iceConnectionState}`);
  });

  pc.addEventListener("icecandidate", (ev) => {
    if (!ev.candidate) return;
    try {
      conn.send({
        type: "candidate",
        connectionId,
        candidate: {
          candidate: ev.candidate.candidate,
          sdpMid: ev.candidate.sdpMid,
          sdpMLineIndex: ev.candidate.sdpMLineIndex,
        },
      });
    } catch (e) {
      log("w", "发送 candidate 失败: " + e);
    }
  });

  function addCandidate(c) {
    const init = {
      candidate: c.candidate,
      sdpMid: c.sdpMid ?? null,
      sdpMLineIndex: c.sdpMLineIndex ?? null,
    };
    if (!remoteSet) {
      earlyCandidates.push(init);
      return;
    }
    pc.addIceCandidate(init).catch((e) => log("w", "addIceCandidate 失败: " + e));
  }

  function onSignalRaw(raw) {
    let msg = raw;
    if (typeof raw === "string") {
      try {
        msg = JSON.parse(raw);
      } catch {
        return;
      }
    }
    if (!msg || !msg.type) return;

    if (msg.type === "answer") {
      pc.setRemoteDescription({ type: "answer", sdp: msg.sdp })
        .then(() => {
          remoteSet = true;
          log("o", "第 2 条通道：answer 已应用");
          earlyCandidates.splice(0).forEach((c) => {
            pc.addIceCandidate(c).catch((e) => log("w", "补加 candidate 失败: " + e));
          });
        })
        .catch((err) => {
          failure = err;
          log("e", "setRemoteDescription 失败: " + err);
        });
    } else if (msg.type === "candidate" && msg.candidate) {
      addCandidate(msg.candidate);
    } else if (msg.type === "error") {
      failure = new Error(msg.message || "节点回报信令错误");
      log("e", failure.message);
    }
  }

  conn.on("data", onSignalRaw);

  const opened = new Promise((resolve, reject) => {
    dc.addEventListener("open", () => resolve(), { once: true });
    dc.addEventListener("error", () => reject(new Error("DataChannel error")), { once: true });
  });

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  conn.send({ type: "offer", connectionId, sdp: offer.sdp });
  log("", "第 2 条通道：offer 已通过 PeerJS 通道发出 · " + connectionId);

  await Promise.race([
    opened,
    new Promise((_, reject) =>
      setTimeout(() => reject(failure || new Error("第 2 条 DataChannel 30s 未打开")), timeout),
    ),
  ]);
  log("o", "第 2 条 WebRTC 数据通道就绪");

  return { pc, dc, connectionId };
}