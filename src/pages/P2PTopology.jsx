// P2P 网络拓扑可视化 — 以 SVG 图形展示连接的对等节点及其状态
import React, { useState, useEffect, useCallback, useRef } from 'react';
import * as api from '../api';

// 将 peer_id 截断为简短标识
function shortID(id, len = 12) {
  if (!id) return '';
  return id.length > len ? id.substring(0, len) + '...' : id;
}

// 根据延迟(ms)返回颜色类名
function latencyColor(ms) {
  if (ms == null) return '#6b7280';
  if (ms < 50) return '#34d399';
  if (ms < 150) return '#facc15';
  return '#f87171';
}

// 格式化字节数
function formatBytes(bytes) {
  if (bytes == null) return '0 B';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  let val = bytes;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return val.toFixed(i > 0 ? 1 : 0) + ' ' + units[i];
}

// 根据质量分数返回颜色
function qualityColor(score) {
  if (score == null) return '#6b7280';
  if (score >= 80) return '#34d399';
  if (score >= 50) return '#facc15';
  return '#f87171';
}

export default function P2PTopology() {
  const [topology, setTopology] = useState(null);
  const [quality, setQuality] = useState([]);
  const [selectedPeer, setSelectedPeer] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [lastRefresh, setLastRefresh] = useState(null);
  const svgRef = useRef(null);
  const refreshTimerRef = useRef(null);

  const fetchTopology = useCallback(async () => {
    try {
      const [topo, qual] = await Promise.all([
        api.getP2PTopology(),
        api.getP2PQuality(),
      ]);
      setTopology(topo);
      setQuality(Array.isArray(qual) ? qual : []);
      setLastRefresh(new Date());
      setLoading(false);
      setError(null);
    } catch (e) {
      setError(e.message);
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchTopology();
    refreshTimerRef.current = setInterval(fetchTopology, 5000);
    return () => {
      if (refreshTimerRef.current) clearInterval(refreshTimerRef.current);
    };
  }, [fetchTopology]);

  // Build graph layout data for SVG rendering.
  // Place the local node at center, peers arranged in a circle around it.
  const graphData = useCallback(() => {
    if (!topology) return { nodes: [], edges: [] };
    const edges = topology.edges || [];
    const count = edges.length;

    // Local node at center
    const localNode = {
      id: 'local',
      peer_id: topology.local_peer_id || '',
      label: shortID(topology.local_peer_id, 16) || 'Local Node',
      x: 350,
      y: 250,
      isLocal: true,
      addrs: topology.local_addrs || [],
    };

    if (count === 0) {
      return { nodes: [localNode], edges: [] };
    }

    // Arrange peers in a circle around the center
    const radius = Math.min(200, 80 + count * 30);
    const peerNodes = edges.map((edge, i) => {
      const angle = (2 * Math.PI * i) / count - Math.PI / 2;
      const x = 350 + radius * Math.cos(angle);
      const y = 250 + radius * Math.sin(angle);

      // Find matching quality data
      const q = quality.find(q => q.peer_id === edge.peer_id);
      const latency = edge.latency || (q ? q.avg_latency_ms + 'ms' : null);
      const latencyNum = latency ? parseFloat(latency) : null;

      return {
        id: edge.peer_id,
        peer_id: edge.peer_id,
        label: shortID(edge.peer_id),
        x,
        y,
        isLocal: false,
        direction: edge.direction || 'unknown',
        latency: latency,
        latencyNum: latencyNum,
        bytes_sent: edge.bytes_sent || 0,
        bytes_recv: edge.bytes_recv || 0,
        connected_since: edge.connected_since || '',
        transport: edge.transport || 'unknown',
        quality: q || null,
        qualityScore: q ? q.score : null,
      };
    });

    // Build edge lines from center to peers
    const edgeLines = peerNodes.map(node => ({
      from: localNode,
      to: node,
      key: node.peer_id,
    }));

    return { nodes: [localNode, ...peerNodes], edges: edgeLines };
  }, [topology, quality]);

  const { nodes, edges: graphEdges } = graphData();

  // Handle click on a peer node
  const handleNodeClick = (node) => {
    if (!node.isLocal) {
      setSelectedPeer(node);
    }
  };

  const closeDetail = () => {
    setSelectedPeer(null);
  };

  if (loading) {
    return (
      <div className="h-full flex items-center justify-center bg-gray-950">
        <div className="text-gray-500 text-sm">加载拓扑数据中...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="h-full flex items-center justify-center bg-gray-950">
        <div className="text-red-400 text-sm">加载失败: {error}</div>
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto bg-gray-950">
      <div className="max-w-6xl mx-auto p-4 md:p-6 space-y-4">

        {/* Header */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <h1 className="text-xl font-bold text-gray-100">网络拓扑</h1>
            <span className="flex items-center gap-1.5 text-xs text-emerald-400">
              <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
              实时
            </span>
          </div>
          <div className="flex items-center gap-3 text-xs text-gray-500">
            {lastRefresh && <span>更新于 {lastRefresh.toLocaleTimeString()}</span>}
            <div className="flex items-center gap-2 text-[10px]">
              <span className="flex items-center gap-1">
                <span className="w-2 h-2 rounded-full bg-emerald-400" /> &lt;50ms
              </span>
              <span className="flex items-center gap-1">
                <span className="w-2 h-2 rounded-full bg-yellow-400" /> &lt;150ms
              </span>
              <span className="flex items-center gap-1">
                <span className="w-2 h-2 rounded-full bg-red-400" /> &ge;150ms
              </span>
            </div>
          </div>
        </div>

        {/* Summary cards */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-3">
            <div className="text-gray-500 text-xs">总节点数</div>
            <div className="text-2xl font-bold text-blue-400">{topology?.total_peers ?? 0}</div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-3">
            <div className="text-gray-500 text-xs">出站连接</div>
            <div className="text-2xl font-bold text-amber-400">
              {(topology?.edges || []).filter(e => e.direction === 'outbound').length}
            </div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-3">
            <div className="text-gray-500 text-xs">入站连接</div>
            <div className="text-2xl font-bold text-purple-400">
              {(topology?.edges || []).filter(e => e.direction === 'inbound').length}
            </div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-3">
            <div className="text-gray-500 text-xs">总传输量</div>
            <div className="text-2xl font-bold text-emerald-400">
              {formatBytes((topology?.edges || []).reduce((sum, e) => sum + (e.bytes_sent || 0) + (e.bytes_recv || 0), 0))}
            </div>
          </div>
        </div>

        {/* SVG Graph */}
        {nodes.length > 0 ? (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <svg
              ref={svgRef}
              viewBox="0 0 700 500"
              className="w-full h-auto"
              style={{ minHeight: '400px' }}
            >
              {/* Edges (connection lines) */}
              {graphEdges.map(edge => {
                const latencyNum = edge.to.latencyNum;
                const strokeColor = latencyColor(latencyNum);
                return (
                  <g key={edge.key}>
                    {/* Main line */}
                    <line
                      x1={edge.from.x} y1={edge.from.y}
                      x2={edge.to.x} y2={edge.to.y}
                      stroke={strokeColor}
                      strokeWidth={edge.to.bytes_sent + edge.to.bytes_recv > 0 ? '2.5' : '1.5'}
                      strokeOpacity="0.6"
                      strokeDasharray={edge.to.direction === 'inbound' ? '6,3' : 'none'}
                    />
                    {/* Direction arrow (outbound only) */}
                    {edge.to.direction === 'outbound' && (
                      <g>
                        <line
                          x1={edge.from.x} y1={edge.from.y}
                          x2={(edge.from.x + edge.to.x) / 2}
                          y2={(edge.from.y + edge.to.y) / 2}
                          stroke={strokeColor}
                          strokeWidth="2"
                          strokeOpacity="0.3"
                          markerEnd="url(#arrowhead)"
                        />
                      </g>
                    )}
                  </g>
                );
              })}

              {/* Arrow marker definition */}
              <defs>
                <marker id="arrowhead" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">
                  <polygon points="0 0, 8 3, 0 6" fill="#facc15" fillOpacity="0.5" />
                </marker>
                <radialGradient id="localGlow" cx="50%" cy="50%" r="50%">
                  <stop offset="0%" stopColor="#60a5fa" stopOpacity="0.3" />
                  <stop offset="100%" stopColor="#60a5fa" stopOpacity="0" />
                </radialGradient>
                <radialGradient id="peerGlow" cx="50%" cy="50%" r="50%">
                  <stop offset="0%" stopColor="#34d399" stopOpacity="0.2" />
                  <stop offset="100%" stopColor="#34d399" stopOpacity="0" />
                </radialGradient>
              </defs>

              {/* Peer nodes */}
              {nodes.map(node => {
                const isLocal = node.isLocal;
                const fillColor = isLocal ? '#3b82f6' : '#1f2937';
                const strokeColor = isLocal ? '#60a5fa' : latencyColor(node.latencyNum);
                const glowId = isLocal ? 'url(#localGlow)' : 'url(#peerGlow)';
                const r = isLocal ? 28 : 22;
                const labelY = isLocal ? -35 : -30;

                return (
                  <g
                    key={node.id}
                    onClick={() => handleNodeClick(node)}
                    style={{ cursor: isLocal ? 'default' : 'pointer' }}
                    className={node.isLocal ? '' : 'hover:opacity-80 transition-opacity'}
                  >
                    {/* Glow */}
                    <circle cx={node.x} cy={node.y} r={r + 20} fill={glowId} />

                    {/* Main circle */}
                    <circle
                      cx={node.x} cy={node.y} r={r}
                      fill={fillColor}
                      stroke={strokeColor}
                      strokeWidth={3}
                    />

                    {/* Inner dot for peers */}
                    {!isLocal && (
                      <circle cx={node.x} cy={node.y} r="4" fill={strokeColor} />
                    )}
                    {/* Center plus for local */}
                    {isLocal && (
                      <>
                        <line x1={node.x - 6} y1={node.y} x2={node.x + 6} y2={node.y} stroke="#93c5fd" strokeWidth="2" />
                        <line x1={node.x} y1={node.y - 6} x2={node.x} y2={node.y + 6} stroke="#93c5fd" strokeWidth="2" />
                      </>
                    )}

                    {/* Label */}
                    <text
                      x={node.x} y={node.y + labelY}
                      textAnchor="middle"
                      className="fill-gray-300 text-xs font-mono"
                      fontSize="11"
                    >
                      {node.label}
                    </text>

                    {/* Direction or quality tag */}
                    {!isLocal && (
                      <text
                        x={node.x} y={node.y + r + 16}
                        textAnchor="middle"
                        className="fill-gray-500"
                        fontSize="9"
                      >
                        {node.direction === 'outbound' ? '->' : '<-'}
                        {node.latency ? ' ' + node.latency : ''}
                      </text>
                    )}

                    {/* Quality score ring */}
                    {!isLocal && node.qualityScore != null && (
                      <circle
                        cx={node.x} cy={node.y} r={r + 6}
                        fill="none"
                        stroke={qualityColor(node.qualityScore)}
                        strokeWidth="1.5"
                        strokeDasharray="4,3"
                        opacity="0.5"
                      />
                    )}
                  </g>
                );
              })}

              {/* Legend */}
              <g transform="translate(20, 460)">
                <text x="0" y="0" className="fill-gray-500" fontSize="9">圆点: 节点</text>
                <text x="120" y="0" className="fill-gray-500" fontSize="9">实线: 出站</text>
                <text x="230" y="0" className="fill-gray-500" fontSize="9">虚线: 入站</text>
                <text x="340" y="0" className="fill-gray-500" fontSize="9">外圈: 质量分</text>
                <text x="450" y="0" className="fill-gray-500" fontSize="9">点击节点查看详情</text>
              </g>
            </svg>
          </div>
        ) : (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-12 text-center">
            <svg className="w-16 h-16 mx-auto mb-4 text-gray-700" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M4 7h.01M7 4h.01M17 4h.01M20 7h.01M4 17h.01M20 17h.01M17 20h.01M7 20h.01M12 12h.01M12 16h.01M16 12h.01M8 12h.01M12 8h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <p className="text-gray-600 text-sm">暂无 P2P 连接</p>
            <p className="text-gray-700 text-xs mt-1">启动 P2P 服务或将节点连接到网络后，此处将显示拓扑图</p>
          </div>
        )}

        {/* Peer List */}
        {nodes.length > 1 && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-3">
              对等节点列表
              <span className="text-gray-500 font-normal ml-1.5">({nodes.length - 1})</span>
            </h2>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
              {nodes.filter(n => !n.isLocal).map((node, i) => (
                <div
                  key={node.id}
                  onClick={() => handleNodeClick(node)}
                  className="bg-gray-800/50 rounded-lg p-3 flex items-center justify-between gap-2 cursor-pointer hover:bg-gray-800 transition-colors"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span
                        className="w-2 h-2 rounded-full shrink-0"
                        style={{ backgroundColor: latencyColor(node.latencyNum) }}
                      />
                      <span className="text-xs font-mono text-gray-300 truncate">{shortID(node.peer_id, 20)}</span>
                    </div>
                    <div className="flex items-center gap-3 mt-1 ml-4 text-[10px] text-gray-500">
                      <span>{node.direction === 'outbound' ? '出站' : '入站'}</span>
                      <span>{node.transport}</span>
                      {node.latency && <span>{node.latency}</span>}
                      {node.qualityScore != null && (
                        <span style={{ color: qualityColor(node.qualityScore) }}>
                          质量 {node.qualityScore.toFixed(0)}
                        </span>
                      )}
                    </div>
                  </div>
                  <div className="text-right text-[10px] text-gray-600 shrink-0">
                    <div>↑ {formatBytes(node.bytes_sent)}</div>
                    <div>↓ {formatBytes(node.bytes_recv)}</div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Quality Metrics */}
        {quality.length > 0 && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-3">连接质量</h2>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
              {quality.map((q, i) => (
                <div key={q.peer_id || i} className="bg-gray-800/50 rounded-lg p-3">
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-xs font-mono text-gray-300 truncate">{shortID(q.peer_id, 20)}</span>
                    <span
                      className="text-[10px] px-2 py-0.5 rounded-full font-mono"
                      style={{
                        backgroundColor: qualityColor(q.score) + '20',
                        color: qualityColor(q.score),
                      }}
                    >
                      {q.score ? q.score.toFixed(0) + '分' : '-'}
                    </span>
                  </div>
                  <div className="flex items-center gap-3 text-[10px] text-gray-500">
                    <span>延迟: {q.avg_latency_ms ? q.avg_latency_ms.toFixed(1) + 'ms' : '-'}</span>
                    <span>抖动: {q.jitter_ms ? q.jitter_ms.toFixed(1) + 'ms' : '-'}</span>
                    <span>采样: {q.samples || 0}</span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Peer Detail Panel */}
        {selectedPeer && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
            onClick={closeDetail}>
            <div className="bg-gray-900 border border-gray-700 rounded-xl p-6 max-w-lg w-full mx-4 shadow-2xl"
              onClick={e => e.stopPropagation()}>
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-sm font-medium text-gray-200">对等节点详情</h3>
                <button onClick={closeDetail}
                  className="text-gray-500 hover:text-gray-300 text-xl leading-none">&times;</button>
              </div>

              <div className="space-y-3 text-xs">
                <div>
                  <div className="text-gray-500 mb-1">Peer ID</div>
                  <div className="font-mono text-gray-200 bg-gray-950 rounded-lg px-3 py-2 break-all border border-gray-800">
                    {selectedPeer.peer_id}
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <div className="text-gray-500 mb-1">方向</div>
                    <div className="text-gray-200">
                      {selectedPeer.direction === 'outbound' ? '出站 (Outbound)' : '入站 (Inbound)'}
                    </div>
                  </div>
                  <div>
                    <div className="text-gray-500 mb-1">传输协议</div>
                    <div className="text-gray-200">{selectedPeer.transport || 'unknown'}</div>
                  </div>
                  <div>
                    <div className="text-gray-500 mb-1">延迟 (RTT)</div>
                    <div className="text-gray-200 flex items-center gap-2">
                      <span className={`w-2 h-2 rounded-full`}
                        style={{ backgroundColor: latencyColor(selectedPeer.latencyNum) }} />
                      {selectedPeer.latency || '未测量'}
                    </div>
                  </div>
                  <div>
                    <div className="text-gray-500 mb-1">连接时长</div>
                    <div className="text-gray-200">{selectedPeer.connected_since || '-'}</div>
                  </div>
                </div>

                <div>
                  <div className="text-gray-500 mb-1">数据传输量</div>
                  <div className="flex items-center gap-4">
                    <div className="text-emerald-400">↑ 发送 {formatBytes(selectedPeer.bytes_sent)}</div>
                    <div className="text-blue-400">↓ 接收 {formatBytes(selectedPeer.bytes_recv)}</div>
                  </div>
                </div>

                {selectedPeer.quality && (
                  <div>
                    <div className="text-gray-500 mb-1">连接质量</div>
                    <div className="grid grid-cols-3 gap-3">
                      <div className="bg-gray-800/50 rounded-lg p-2 text-center">
                        <div className="text-lg font-bold"
                          style={{ color: qualityColor(selectedPeer.quality.score) }}>
                          {selectedPeer.quality.score ? selectedPeer.quality.score.toFixed(0) : '-'}
                        </div>
                        <div className="text-[10px] text-gray-500">综合评分</div>
                      </div>
                      <div className="bg-gray-800/50 rounded-lg p-2 text-center">
                        <div className="text-lg font-bold text-gray-200">
                          {selectedPeer.quality.avg_latency_ms
                            ? selectedPeer.quality.avg_latency_ms.toFixed(1)
                            : '-'}
                        </div>
                        <div className="text-[10px] text-gray-500">平均延迟 (ms)</div>
                      </div>
                      <div className="bg-gray-800/50 rounded-lg p-2 text-center">
                        <div className="text-lg font-bold text-gray-200">
                          {selectedPeer.quality.jitter_ms
                            ? selectedPeer.quality.jitter_ms.toFixed(1)
                            : '-'}
                        </div>
                        <div className="text-[10px] text-gray-500">抖动 (ms)</div>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
