// WebRTC 对等连接模块：通过信令服务器管理多对等连接和数据通道
import React, { useState, useEffect, useRef } from 'react';
import * as api from '../api';

const WS_SIGNAL_URL = (api.WS_TRANSFER_URL_BASE?.() || 'wss://wsl-3000.moonchan.xyz').replace(/\/ws\/transfer$/, '/ws/signal');

const ICE_SERVERS = {
  iceServers: [
    { urls: 'stun:stun.moonchan.xyz:3478' },
    { urls: 'stun:stun.l.google.com:19302' },
  ],
};

export default function WebRTCPeer({ peerId, onFileReceived }) {
  const [connected, setConnected] = useState(false);
  const [peers, setPeers] = useState([]);
  const [log, setLog] = useState([]);
  const wsRef = useRef(null);
  const pcsRef = useRef({}); // peerID -> RTCPeerConnection
  const dcsRef = useRef({}); // peerID -> DataChannel

  const addLog = (msg) => setLog(prev => [...prev.slice(-99), msg]);

  useEffect(() => {
    const ws = new WebSocket(WS_SIGNAL_URL);
    wsRef.current = ws;

    ws.onopen = () => {
      addLog('信令已连接');
      ws.send(JSON.stringify({ type: 'register', peer_id: peerId }));
    };

    ws.onmessage = async (e) => {
      const msg = JSON.parse(e.data);
      switch (msg.type) {
        case 'registered':
          setConnected(true);
          addLog(`已注册: ${peerId}`);
          ws.send(JSON.stringify({ type: 'request_peers' }));
          break;

        case 'peers':
          setPeers(msg.peers.filter(p => p !== peerId));
          addLog(`在线节点: ${msg.peers.length}`);
          break;

        case 'peer_joined':
          if (msg.peer_id !== peerId) {
            setPeers(prev => [...new Set([...prev, msg.peer_id])]);
            addLog(`节点加入: ${msg.peer_id.slice(0, 12)}...`);
          }
          break;

        case 'peer_left':
          setPeers(prev => prev.filter(p => p !== msg.peer_id));
          cleanupPeer(msg.peer_id);
          addLog(`节点离开: ${msg.peer_id.slice(0, 12)}...`);
          break;

        case 'offer':
          await handleOffer(msg.from, msg.sdp);
          break;

        case 'answer':
          await handleAnswer(msg.from, msg.sdp);
          break;

        case 'ice_candidate':
          await handleIceCandidate(msg.from, msg.candidate);
          break;

        case 'file_providers':
          addLog(`文件 ${msg.hash.slice(0, 12)}... 提供者: ${msg.peers?.length || 0} 个`);
          break;
      }
    };

    ws.onclose = () => { setConnected(false); addLog('信令断开'); };
    ws.onerror = () => addLog('信令错误');

    return () => { ws.close(); cleanupAll(); };
  }, [peerId]);

  const cleanupPeer = (pid) => {
    if (dcsRef.current[pid]) { dcsRef.current[pid].close(); delete dcsRef.current[pid]; }
    if (pcsRef.current[pid]) { pcsRef.current[pid].close(); delete pcsRef.current[pid]; }
  };

  const cleanupAll = () => {
    Object.keys(dcsRef.current).forEach(cleanupPeer);
  };

  // Create RTCPeerConnection for a peer
  const createPC = (targetPeerId) => {
    const pc = new RTCPeerConnection(ICE_SERVERS);
    pcsRef.current[targetPeerId] = pc;

    pc.onicecandidate = (e) => {
      if (e.candidate) {
        wsRef.current?.send(JSON.stringify({
          type: 'ice_candidate', to: targetPeerId, peer_id: peerId,
          candidate: JSON.stringify(e.candidate),
        }));
      }
    };

    pc.ondatachannel = (e) => {
      setupDataChannel(targetPeerId, e.channel);
    };

    // Create data channel for file transfer
    const dc = pc.createDataChannel('file_transfer', { ordered: true });
    setupDataChannel(targetPeerId, dc);

    return pc;
  };

  const setupDataChannel = (peerId, dc) => {
    dcsRef.current[peerId] = dc;
    dc.onopen = () => addLog(`通道建立: ${peerId.slice(0, 12)}...`);
    dc.onclose = () => addLog(`通道关闭: ${peerId.slice(0, 12)}...`);
    dc.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'request') {
          addLog(`收到文件请求: ${msg.hash.slice(0, 12)}...`);
        } else if (msg.type === 'response') {
          addLog(`收到文件响应: ${msg.hash.slice(0, 12)}... (${msg.size} B)`);
        }
        if (onFileReceived) onFileReceived(msg);
      } catch {
        addLog(`收到二进制数据: ${e.data.byteLength || e.data.size || '?'} B`);
      }
    };
  };

  const handleOffer = async (from, sdp) => {
    const pc = createPC(from);
    await pc.setRemoteDescription({ type: 'offer', sdp });
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    wsRef.current?.send(JSON.stringify({
      type: 'answer', to: from, peer_id: peerId, sdp: answer.sdp,
    }));
  };

  const handleAnswer = async (from, sdp) => {
    const pc = pcsRef.current[from];
    if (pc) await pc.setRemoteDescription({ type: 'answer', sdp });
  };

  const handleIceCandidate = async (from, candidateStr) => {
    try {
      const candidate = JSON.parse(candidateStr);
      const pc = pcsRef.current[from];
      if (pc) await pc.addIceCandidate(candidate);
    } catch {}
  };

  const connectToPeer = async (targetId) => {
    if (pcsRef.current[targetId]) return;
    const pc = createPC(targetId);
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    wsRef.current?.send(JSON.stringify({
      type: 'offer', to: targetId, peer_id: peerId, sdp: offer.sdp,
    }));
    addLog(`发起连接: ${targetId.slice(0, 12)}...`);
  };

  const requestFile = async (targetId, hash) => {
    const dc = dcsRef.current[targetId];
    if (dc && dc.readyState === 'open') {
      dc.send(JSON.stringify({ type: 'request', hash }));
      addLog(`请求文件: ${hash.slice(0, 12)}...`);
    } else {
      addLog('通道未就绪，先连接');
      await connectToPeer(targetId);
    }
  };

  const announceFile = (hash) => {
    wsRef.current?.send(JSON.stringify({ type: 'announce_file', peer_id: peerId, hash }));
  };

  const findFile = (hash) => {
    wsRef.current?.send(JSON.stringify({ type: 'find_file', peer_id: peerId, hash }));
  };

  return { connected, peers, log, connectToPeer, requestFile, announceFile, findFile };
}
