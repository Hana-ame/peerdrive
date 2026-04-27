import React, { useState, useEffect, useRef, useCallback } from 'react';

const CHUNK_SIZE = 16384; // 16KB data channel chunks

/**
 * WebRTCTransfer provides direct browser-to-browser file transfer via WebRTC.
 *
 * Props:
 *   fileHash   - SHA-256 hash of the file to share
 *   fileName   - Display name for the file
 *   fileSize   - Size in bytes (optional, for progress display)
 *   onClose    - Called when the user dismisses the panel
 *   onError    - Called with error message when fallback is needed
 *
 * The component:
 *  1. Fetches STUN/TURN config from the relay server
 *  2. Joins a signaling room identified by fileHash
 *  3. Initiates or accepts a WebRTC DataChannel connection
 *  4. Transfers the file as ordered binary chunks
 *  5. Reports progress via a progress bar
 *  6. Calls onError if WebRTC setup fails (so the caller can fall back)
 */
export default function WebRTCTransfer({ fileHash, fileName, fileSize, onClose, onError }) {
  const [status, setStatus] = useState('initializing'); // initializing | connecting | connected | transferring | done | error
  const [progress, setProgress] = useState(0);
  const [log, setLog] = useState([]);
  const [signalConnected, setSignalConnected] = useState(false);
  const [remotePeer, setRemotePeer] = useState(null);
  const [transferStats, setTransferStats] = useState({ sent: 0, received: 0 });

  const wsRef = useRef(null);
  const pcRef = useRef(null);
  const dcRef = useRef(null);
  const localPeerId = useRef('peer_' + Math.random().toString(36).substring(2, 10));
  const pendingFileRef = useRef(null);
  const chunksSentRef = useRef(0);
  const chunksTotalRef = useRef(0);
  const receiveBufferRef = useRef([]);
  const receiveLengthRef = useRef(0);
  const expectedSizeRef = useRef(0);

  const addLog = useCallback((msg) => {
    setLog(prev => [...prev.slice(-49), `[${new Date().toLocaleTimeString()}] ${msg}`]);
  }, []);

  // Fetch STUN/TURN config from relay
  const getIceConfig = useCallback(async () => {
    try {
      const base = getApiBase();
      const res = await fetch(`${base}/p2p/webrtc/info`);
      if (!res.ok) throw new Error('WebRTC info unavailable');
      const info = await res.json();
      const servers = [];
      if (info.stun_server) {
        const url = info.stun_server.startsWith('stun:')
          ? info.stun_server
          : `stun:${info.stun_server}`;
        servers.push({ urls: url });
      }
      // Always include Google STUN as a fallback
      servers.push({ urls: 'stun:stun.l.google.com:19302' });
      if (info.turn_server) {
        servers.push({
          urls: info.turn_server.startsWith('turn:') ? info.turn_server : `turn:${info.turn_server}`,
          username: info.turn_username || '',
          credential: info.turn_credential || '',
        });
      }
      return { iceServers: servers };
    } catch (e) {
      addLog('STUN config fetch failed, using defaults: ' + e.message);
      return {
        iceServers: [
          { urls: 'stun:stun.l.google.com:19302' },
        ],
      };
    }
  }, [addLog]);

  function getApiBase() {
    try {
      return localStorage.getItem('peerdrive_api_base') || 'https://wsl-3000.moonchan.xyz';
    } catch {
      return 'https://wsl-3000.moonchan.xyz';
    }
  }

  function getWsBase() {
    const base = getApiBase().replace(/^http/, 'ws');
    return base;
  }

  // Open signaling WebSocket and join room
  useEffect(() => {
    if (!fileHash) return;

    let cancelled = false;
    const wsUrl = `${getWsBase()}/ws/signal`;
    addLog(`Connecting to signaling server...`);

    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      if (cancelled) return;
      setSignalConnected(true);
      addLog('Signaling connected, joining room...');
      ws.send(JSON.stringify({
        type: 'join',
        peer_id: localPeerId.current,
        hash: fileHash,
      }));
    };

    ws.onmessage = async (e) => {
      if (cancelled) return;
      try {
        const msg = JSON.parse(e.data);
        await handleSignalMessage(msg);
      } catch (err) {
        addLog('Signal parse error: ' + err.message);
      }
    };

    ws.onclose = () => {
      if (!cancelled) {
        setSignalConnected(false);
        addLog('Signaling disconnected');
      }
    };

    ws.onerror = () => {
      if (!cancelled) {
        addLog('Signaling connection error');
        setStatus('error');
        if (onError) onError('WebRTC signaling connection failed');
      }
    };

    return () => {
      cancelled = true;
      ws.close();
    };
  }, [fileHash]);

  // Handle incoming signaling messages
  const handleSignalMessage = useCallback(async (msg) => {
    const iceConfig = await getIceConfig();

    switch (msg.type) {
      case 'room_joined':
        setStatus('connecting');
        addLog(`Joined room for ${fileHash ? fileHash.substring(0, 12) : '?'}...`);

        // If there are other peers in the room, initiate connection
        const otherPeers = (msg.peers || []).filter(p => p !== localPeerId.current);
        if (otherPeers.length > 0) {
          const target = otherPeers[0];
          setRemotePeer(target);
          addLog(`Found peer: ${target.substring(0, 12)}...`);
          await initiateConnection(target, iceConfig);
        } else {
          addLog('Waiting for a peer to join...');
        }
        break;

      case 'peer_joined_room':
        if (msg.peer_id !== localPeerId.current) {
          const peerId = msg.peer_id;
          setRemotePeer(peerId);
          addLog(`Peer joined: ${peerId.substring(0, 12)}...`);
          await initiateConnection(peerId, iceConfig);
        }
        break;

      case 'offer':
        await handleOffer(msg, iceConfig);
        break;

      case 'answer':
        await handleAnswer(msg);
        break;

      case 'ice_candidate':
        await handleIceCandidate(msg);
        break;

      case 'peer_left':
        if (msg.peer_id === remotePeer) {
          addLog('Remote peer disconnected');
          setRemotePeer(null);
          setStatus('error');
          if (onError) onError('Remote peer disconnected');
        }
        break;

      default:
        addLog(`Unhandled signal: ${msg.type}`);
    }
  }, [fileHash, remotePeer, addLog, getIceConfig, onError]);

  const createPeerConnection = useCallback((iceConfig) => {
    const pc = new RTCPeerConnection(iceConfig);

    pc.oniceconnectionstatechange = () => {
      addLog(`ICE state: ${pc.iceConnectionState}`);
      if (pc.iceConnectionState === 'failed' || pc.iceConnectionState === 'disconnected') {
        setStatus('error');
        if (onError) onError('WebRTC connection failed - ICE state: ' + pc.iceConnectionState);
      }
    };

    pc.onsignalingstatechange = () => {
      addLog(`Signaling state: ${pc.signalingState}`);
    };

    pc.onicecandidate = (e) => {
      if (e.candidate && wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(JSON.stringify({
          type: 'ice',
          to: remotePeer,
          peer_id: localPeerId.current,
          candidate: JSON.stringify(e.candidate),
        }));
      }
    };

    pc.ondatachannel = (e) => {
      addLog('Incoming data channel');
      setupDataChannel(e.channel);
    };

    return pc;
  }, [remotePeer, addLog, onError]);

  const setupDataChannel = useCallback((dc) => {
    dcRef.current = dc;

    dc.onopen = () => {
      setStatus('connected');
      addLog('Data channel open');
    };

    dc.onclose = () => {
      addLog('Data channel closed');
    };

    dc.onmessage = (e) => {
      handleDataChannelMessage(e);
    };
  }, [addLog]);

  const handleDataChannelMessage = useCallback((e) => {
    if (typeof e.data === 'string') {
      // JSON metadata message
      try {
        const meta = JSON.parse(e.data);
        switch (meta.type) {
          case 'file_meta':
            expectedSizeRef.current = meta.size || 0;
            receiveBufferRef.current = [];
            receiveLengthRef.current = 0;
            setStatus('transferring');
            addLog(`Receiving: ${meta.name || 'file'} (${formatSize(meta.size || 0)})`);
            break;

          case 'transfer_complete':
            // All chunks received, assemble and save
            assembleReceivedFile();
            break;

          case 'error':
            addLog(`Remote error: ${meta.message}`);
            break;
        }
      } catch { /* binary data will be handled below */ }
    } else {
      // Binary chunk
      receiveBufferRef.current.push(e.data);
      receiveLengthRef.current += e.data.byteLength || e.data.size || 0;
      chunksSentRef.current++;

      if (expectedSizeRef.current > 0) {
        const pct = Math.min(100, Math.round((receiveLengthRef.current / expectedSizeRef.current) * 100));
        setProgress(pct);
        setTransferStats(prev => ({ ...prev, received: receiveLengthRef.current }));
      }
    }
  }, [addLog]);

  const assembleReceivedFile = useCallback(() => {
    const blobs = receiveBufferRef.current;
    const totalLength = receiveLengthRef.current;
    const blob = new Blob(blobs);
    const url = URL.createObjectURL(blob);

    // Trigger download in browser
    const a = document.createElement('a');
    a.href = url;
    a.download = fileName || `webrtc-${fileHash ? fileHash.substring(0, 8) : 'file'}`;
    a.click();
    URL.revokeObjectURL(url);

    setProgress(100);
    setStatus('done');
    setTransferStats(prev => ({ ...prev, received: totalLength }));
    addLog(`File received: ${formatSize(totalLength)}`);
  }, [fileName, fileHash, addLog]);

  const initiateConnection = useCallback(async (target, iceConfig) => {
    if (pcRef.current) {
      addLog('Already have a peer connection');
      return;
    }

    setStatus('connecting');
    const pc = createPeerConnection(iceConfig);
    pcRef.current = pc;

    // Create outgoing data channel
    const dc = pc.createDataChannel('peerdrive-transfer', { ordered: true });
    setupDataChannel(dc);

    try {
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      wsRef.current?.send(JSON.stringify({
        type: 'offer',
        to: target,
        peer_id: localPeerId.current,
        sdp: offer.sdp,
      }));
      addLog('Offer sent');
    } catch (err) {
      addLog('Create offer error: ' + err.message);
      setStatus('error');
      if (onError) onError('Failed to create WebRTC offer: ' + err.message);
    }
  }, [createPeerConnection, setupDataChannel, addLog, onError]);

  const handleOffer = useCallback(async (msg, iceConfig) => {
    setRemotePeer(msg.from);
    setStatus('connecting');

    const pc = createPeerConnection(iceConfig);
    pcRef.current = pc;

    try {
      await pc.setRemoteDescription({ type: 'offer', sdp: msg.sdp });
      const answer = await pc.createAnswer();
      await pc.setLocalDescription(answer);
      wsRef.current?.send(JSON.stringify({
        type: 'answer',
        to: msg.from,
        peer_id: localPeerId.current,
        sdp: answer.sdp,
      }));
      addLog('Answer sent');
    } catch (err) {
      addLog('Handle offer error: ' + err.message);
    }
  }, [createPeerConnection, addLog]);

  const handleAnswer = useCallback(async (msg) => {
    if (pcRef.current && msg.sdp) {
      try {
        await pcRef.current.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
        addLog('Remote description set');
      } catch (err) {
        addLog('Set remote description error: ' + err.message);
      }
    }
  }, [addLog]);

  const handleIceCandidate = useCallback(async (msg) => {
    if (!msg.candidate) return;
    try {
      const candidate = JSON.parse(msg.candidate);
      if (pcRef.current && candidate) {
        await pcRef.current.addIceCandidate(candidate);
      }
    } catch (err) {
      addLog('ICE candidate error: ' + err.message);
    }
  }, [addLog]);

  // Send a file via the data channel
  const sendFile = useCallback(async (file) => {
    if (!dcRef.current || dcRef.current.readyState !== 'open') {
      addLog('Data channel not ready');
      setStatus('error');
      if (onError) onError('Data channel not ready');
      return;
    }

    const dc = dcRef.current;
    setStatus('transferring');

    // Send file metadata first
    const meta = {
      type: 'file_meta',
      name: file.name,
      size: file.size,
      mime: file.type || 'application/octet-stream',
    };

    try {
      dc.send(JSON.stringify(meta));
      addLog(`Sending: ${file.name} (${formatSize(file.size)})`);

      // Read file and send in chunks
      const reader = new FileReader();
      const totalSize = file.size;
      let offset = 0;
      chunksTotalRef.current = Math.ceil(totalSize / CHUNK_SIZE);
      chunksSentRef.current = 0;

      const sendNextChunk = () => {
        if (offset >= totalSize) {
          dc.send(JSON.stringify({ type: 'transfer_complete' }));
          setProgress(100);
          setStatus('done');
          setTransferStats(prev => ({ ...prev, sent: totalSize }));
          addLog('File sent successfully');
          return;
        }

        const slice = file.slice(offset, offset + CHUNK_SIZE);
        reader.onload = (e) => {
          const chunk = e.target.result;
          try {
            dc.send(chunk);
          } catch (err) {
            addLog(`Send error at ${offset}: ${err.message}`);
            setStatus('error');
            if (onError) onError('Data channel send failed: ' + err.message);
            return;
          }

          offset += chunk.byteLength || chunk.size || 0;
          chunksSentRef.current++;

          const pct = Math.min(100, Math.round((offset / totalSize) * 100));
          setProgress(pct);
          setTransferStats(prev => ({ ...prev, sent: offset }));

          // Schedule next chunk (async to not block UI)
          setTimeout(sendNextChunk, 0);
        };

        reader.onerror = () => {
          addLog('File read error');
          setStatus('error');
        };

        reader.readAsArrayBuffer(slice);
      };

      sendNextChunk();
    } catch (err) {
      addLog(`Send error: ${err.message}`);
      setStatus('error');
      if (onError) onError('WebRTC send failed: ' + err.message);
    }
  }, [addLog, onError]);

  const cleanup = useCallback(() => {
    if (dcRef.current) {
      dcRef.current.close();
      dcRef.current = null;
    }
    if (pcRef.current) {
      pcRef.current.close();
      pcRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => cleanup();
  }, [cleanup]);

  const formatSize = (bytes) => {
    if (!bytes || bytes === 0) return '0 B';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
    if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB';
    return (bytes / 1073741824).toFixed(1) + ' GB';
  };

  const statusDisplay = {
    initializing: { text: 'Initializing...', color: 'text-gray-400' },
    connecting: { text: 'Connecting to peer...', color: 'text-yellow-400' },
    connected: { text: 'Connected', color: 'text-green-400' },
    transferring: { text: 'Transferring...', color: 'text-blue-400' },
    done: { text: 'Transfer complete', color: 'text-green-400' },
    error: { text: 'Error', color: 'text-red-400' },
  }[status] || { text: status, color: 'text-gray-400' };

  return (
    <div className="bg-gray-800 border border-gray-700 rounded-lg p-4 mb-3">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold flex items-center gap-2">
          <span>WebRTC Transfer</span>
          <span className={`inline-block w-2 h-2 rounded-full ${signalConnected ? 'bg-green-400' : 'bg-red-400'}`} />
          <span className={`text-xs ${statusDisplay.color}`}>{statusDisplay.text}</span>
        </h3>
        {onClose && (
          <button onClick={onClose} className="text-gray-500 hover:text-white text-sm px-2">&times;</button>
        )}
      </div>

      {/* Progress bar */}
      {status === 'transferring' && (
        <div className="mb-3">
          <div className="flex justify-between text-xs text-gray-400 mb-1">
            <span>{formatSize(transferStats.sent || transferStats.received)} / {formatSize(fileSize || 0)}</span>
            <span>{progress}%</span>
          </div>
          <div className="w-full bg-gray-700 rounded-full h-2">
            <div
              className="bg-blue-500 h-2 rounded-full transition-all duration-300"
              style={{ width: `${Math.min(100, progress)}%` }}
            />
          </div>
        </div>
      )}

      {status === 'done' && (
        <div className="flex items-center gap-2 text-green-400 text-sm mb-2">
          <span>Transfer complete!</span>
        </div>
      )}

      {/* File input (for sender) */}
      <div className="mb-2">
        <input
          type="file"
          id="webrtc-file-input"
          className="hidden"
          onChange={(e) => {
            if (e.target.files[0]) {
              sendFile(e.target.files[0]);
            }
          }}
        />
        <button
          onClick={() => document.getElementById('webrtc-file-input').click()}
          disabled={status !== 'connected'}
          className={`px-3 py-1.5 rounded text-xs font-medium transition-colors ${
            status === 'connected'
              ? 'bg-blue-600 hover:bg-blue-500 text-white'
              : 'bg-gray-700 text-gray-500 cursor-not-allowed'
          }`}
        >
          Select File to Send
        </button>
      </div>

      {/* Connection info */}
      {remotePeer && (
        <div className="text-xs text-gray-500 mb-2">
          Peer: {remotePeer.substring(0, 16)}...
        </div>
      )}

      {/* Event log */}
      <details className="text-xs">
        <summary className="text-gray-500 cursor-pointer hover:text-gray-300">
          Event Log ({log.length})
        </summary>
        <div className="mt-1 bg-gray-900 rounded p-2 max-h-32 overflow-y-auto font-mono text-[10px] leading-relaxed text-gray-400">
          {log.length === 0 ? (
            <span className="text-gray-600">No events yet</span>
          ) : (
            log.map((entry, i) => <div key={i}>{entry}</div>)
          )}
        </div>
      </details>
    </div>
  );
}

export { CHUNK_SIZE };
