import React, { useState, useEffect, useRef } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import * as ws from '../../../ws';

/**
 * Display — Remote controlled display screen and public presentation page (Issue #243).
 *
 * Requirements:
 * 1. "遥控功能：需要直接控制某个session屏幕上出现什么。该session也需要打开一个受控界面才可以"
 * 2. "遥控功能：session可以打开一个公共的页面，这个页面可以用来被显示什么（大多数时候是图片或者视频）"
 * 3. Supports image rendering, video playback with play/pause/seek control, and live status sync.
 */
export default function Display() {
  const { channel: paramChannel } = useParams();
  const [searchParams] = useSearchParams();

  const channel = paramChannel || searchParams.get('channel') || 'default';
  const initialName = searchParams.get('name') || `Screen-${Math.random().toString(36).slice(2, 6).toUpperCase()}`;

  const [screenId] = useState(() => 'disp-' + Math.random().toString(36).slice(2, 8));
  const [screenName, setScreenName] = useState(initialName);
  const [connected, setConnected] = useState(ws.getStatus() === 'open');
  const [registered, setRegistered] = useState(false);
  const [media, setMedia] = useState(null); // { mediaType, hash, url, title, autoplay, loop }
  const [showHud, setShowHud] = useState(true);
  const [copied, setCopied] = useState(false);
  const [currentTime, setCurrentTime] = useState(new Date());

  const videoRef = useRef(null);
  const audioRef = useRef(null);
  const containerRef = useRef(null);

  // Digital clock update
  useEffect(() => {
    const timer = setInterval(() => setCurrentTime(new Date()), 1000);
    return () => clearInterval(timer);
  }, []);

  // Track WS connection and register screen
  useEffect(() => {
    const unbindStatus = ws.onStatus((st) => {
      setConnected(st === 'open');
      if (st === 'open') {
        registerScreen();
      } else {
        setRegistered(false);
      }
    });

    const registerScreen = () => {
      ws.sendFrame({
        type: 'display',
        action: 'register',
        sessionId: screenId,
        channel,
        name: screenName,
      });
      setRegistered(true);

      const streamParam = searchParams.get('stream');
      if (streamParam) {
        ws.sendFrame({
          type: 'stream',
          action: 'sub',
          streamId: streamParam,
        });
      }

      // Query current state for channel sync
      ws.admin('GET', `/display/status?channel=${encodeURIComponent(channel)}`)
        .then((st) => {
          if (st && st.mediaType) {
            setMedia({
              mediaType: st.mediaType,
              hash: st.hash,
              url: st.url,
              title: st.title,
              autoplay: st.autoplay,
              loop: st.loop,
              position: st.position,
              volume: st.volume,
            });
          }
        })
        .catch(() => {});
    };

    if (ws.getStatus() === 'open') {
      registerScreen();
    }

    return () => {
      unbindStatus();
      if (ws.getStatus() === 'open') {
        ws.sendFrame({
          type: 'display',
          action: 'unregister',
          sessionId: screenId,
        });
        const streamParam = searchParams.get('stream');
        if (streamParam) {
          ws.sendFrame({
            type: 'stream',
            action: 'unsub',
            streamId: streamParam,
          });
        }
      }
    };
  }, [channel, screenId, screenName, searchParams]);

  // Listen to incoming display and stream frames
  useEffect(() => {
    const unbindMessage = ws.onMessage((msg) => {
      if (!msg) return;

      if (msg.type === 'display') {
        if (msg.action === 'show') {
          setMedia({
            mediaType: msg.mediaType || 'image',
            hash: msg.hash,
            url: msg.url,
            title: msg.title,
            autoplay: msg.autoplay !== false,
            loop: !!msg.loop,
            position: msg.position,
            volume: msg.volume,
          });
        } else if (msg.action === 'control') {
          handleControlAction(msg);
        } else if (msg.action === 'clear') {
          setMedia(null);
        }
      } else if (msg.type === 'stream') {
        if (msg.action === 'chunk' && msg.chunk) {
          const chunk = msg.chunk;
          let mType = 'image';
          if (chunk.mimeType?.startsWith('video/') || chunk.hash?.endsWith('.mp4')) mType = 'video';
          else if (chunk.mimeType?.startsWith('audio/')) mType = 'audio';

          setMedia({
            mediaType: mType,
            hash: chunk.hash,
            title: `${chunk.title || 'Live Segment'} #${chunk.seq}`,
            autoplay: true,
            loop: false,
            isLiveStream: true,
            streamId: msg.streamId,
            seq: chunk.seq,
          });
        } else if (msg.action === 'close') {
          setMedia(null);
        }
      }
    });

    return () => unbindMessage();
  }, []);

  // Handle remote playback control
  const handleControlAction = (cmd) => {
    const player = videoRef.current || audioRef.current;
    if (!player) return;

    switch (cmd.controlAction) {
      case 'play':
        player.play().catch(() => {});
        break;
      case 'pause':
      case 'stop':
        player.pause();
        break;
      case 'seek':
        if (typeof cmd.position === 'number') {
          player.currentTime = cmd.position;
        }
        break;
      case 'volume':
        if (typeof cmd.volume === 'number') {
          player.volume = Math.max(0, Math.min(1, cmd.volume));
        }
        break;
      default:
        break;
    }
  };

  // Keyboard shortcuts
  useEffect(() => {
    const onKeyDown = (e) => {
      if (e.target.tagName === 'INPUT') return;

      if (e.key === 'f' || e.key === 'F') {
        toggleFullscreen();
      } else if (e.key === ' ') {
        e.preventDefault();
        const player = videoRef.current || audioRef.current;
        if (player) {
          if (player.paused) player.play().catch(() => {});
          else player.pause();
        }
      } else if (e.key === 'c' || e.key === 'C') {
        setMedia(null);
      } else if (e.key === 'h' || e.key === 'H') {
        setShowHud((prev) => !prev);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  const toggleFullscreen = () => {
    if (!document.fullscreenElement) {
      containerRef.current?.requestFullscreen().catch(() => {});
    } else {
      document.exitFullscreen().catch(() => {});
    }
  };

  const copyScreenId = () => {
    navigator.clipboard?.writeText(screenId).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  // Resolve media source URL (either peerdrive hash download or explicit URL)
  const getMediaSrc = () => {
    if (!media) return '';
    if (media.url) return media.url;
    if (media.hash) {
      return `/files/download/${encodeURIComponent(media.hash)}`;
    }
    return '';
  };

  return (
    <div
      ref={containerRef}
      className="relative w-full h-full min-h-screen bg-neutral-950 text-white flex flex-col items-center justify-center overflow-hidden select-none"
      onMouseMove={() => setShowHud(true)}
    >
      {/* Background ambient lighting */}
      <div className="absolute inset-0 bg-radial-gradient from-brand-900/20 via-neutral-950 to-neutral-950 pointer-events-none" />

      {/* Top Floating HUD */}
      <div
        className={`absolute top-4 left-4 right-4 z-50 flex items-center justify-between transition-opacity duration-300 ${
          showHud ? 'opacity-100' : 'opacity-0 pointer-events-none'
        }`}
      >
        <div className="flex items-center gap-2 bg-neutral-900/80 backdrop-blur-md px-3 py-1.5 rounded-full border border-white/10 text-xs">
          <span
            className={`w-2.5 h-2.5 rounded-full ${
              connected && registered ? 'bg-emerald-500 animate-pulse' : 'bg-red-500'
            }`}
          />
          <span className="font-medium text-gray-200">{screenName}</span>
          <span className="text-gray-500">|</span>
          <span className="text-gray-400">频道: {channel}</span>
          {media?.isLiveStream && (
            <span className="flex items-center gap-1.5 bg-red-500/20 text-red-400 px-2.5 py-0.5 rounded-full border border-red-500/30 text-[11px] font-semibold animate-pulse ml-1">
              <span className="w-1.5 h-1.5 rounded-full bg-red-500" />
              LIVE #{media.seq}
            </span>
          )}
        </div>

        <div className="flex items-center gap-2">
          {media && (
            <button
              onClick={() => setMedia(null)}
              className="bg-neutral-900/80 hover:bg-neutral-800 text-xs text-red-400 px-3 py-1.5 rounded-full border border-white/10 transition"
              title="清空画面 (C)"
            >
              清空画面
            </button>
          )}

          <button
            onClick={copyScreenId}
            className="bg-neutral-900/80 hover:bg-neutral-800 text-xs text-gray-300 px-3 py-1.5 rounded-full border border-white/10 transition"
            title="复制屏幕ID用于定向遥控"
          >
            {copied ? '已复制屏幕ID' : `ID: ${screenId}`}
          </button>

          <button
            onClick={toggleFullscreen}
            className="bg-neutral-900/80 hover:bg-neutral-800 text-xs text-gray-300 p-2 rounded-full border border-white/10 transition"
            title="全屏切换 (F)"
          >
            ⛶
          </button>
        </div>
      </div>

      {/* Main Content Area */}
      {media ? (
        <div className="relative w-full h-full flex items-center justify-center">
          {media.mediaType === 'image' && (
            <div className="relative w-full h-full flex flex-col items-center justify-center p-4">
              <img
                src={getMediaSrc()}
                alt={media.title || 'Cast Image'}
                className="max-w-full max-h-[92vh] object-contain rounded-lg shadow-2xl transition-all duration-300"
              />
              {media.title && (
                <div className="absolute bottom-6 bg-black/60 backdrop-blur-md px-4 py-1.5 rounded-full border border-white/10 text-sm text-gray-200 shadow-lg">
                  {media.title}
                </div>
              )}
            </div>
          )}

          {media.mediaType === 'video' && (
            <div className="relative w-full h-full flex flex-col items-center justify-center">
              <video
                ref={videoRef}
                src={getMediaSrc()}
                autoPlay={media.autoplay !== false}
                loop={media.loop}
                controls
                className="max-w-full max-h-screen object-contain shadow-2xl"
              />
              {media.title && (
                <div className="absolute top-16 left-6 bg-black/60 backdrop-blur-md px-3 py-1 rounded-md text-xs text-gray-300 border border-white/10">
                  {media.title}
                </div>
              )}
            </div>
          )}

          {media.mediaType === 'audio' && (
            <div className="flex flex-col items-center justify-center gap-6 p-8 bg-neutral-900/70 border border-white/10 rounded-2xl backdrop-blur-lg shadow-2xl max-w-md w-full">
              <div className="w-24 h-24 rounded-full bg-brand-500/20 border border-brand-500/30 flex items-center justify-center text-4xl animate-pulse">
                🎵
              </div>
              <div className="text-center">
                <h3 className="text-lg font-semibold text-white">{media.title || '音频播放中'}</h3>
                {media.hash && <p className="text-xs text-gray-400 font-mono mt-1">{media.hash.slice(0, 16)}...</p>}
              </div>
              <audio
                ref={audioRef}
                src={getMediaSrc()}
                autoPlay={media.autoplay !== false}
                loop={media.loop}
                controls
                className="w-full"
              />
            </div>
          )}

          {media.mediaType === 'text' && (
            <div className="max-w-3xl p-12 bg-neutral-900/80 border border-white/10 rounded-2xl backdrop-blur-xl shadow-2xl text-center">
              <h2 className="text-3xl font-bold mb-4 text-brand-300">{media.title || '展示通知'}</h2>
              <p className="text-xl text-gray-200 leading-relaxed">{media.url || media.hash}</p>
            </div>
          )}
        </div>
      ) : (
        /* Standby / Ambient Idle Screen */
        <div className="flex flex-col items-center justify-center text-center p-8 z-10 max-w-xl">
          {/* Clock */}
          <div className="text-6xl md:text-8xl font-thin tracking-wider font-mono text-gray-200 mb-2">
            {currentTime.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
          </div>
          <div className="text-sm md:text-base text-gray-400 mb-10">
            {currentTime.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })}
          </div>

          {/* Screen Card */}
          <div className="bg-neutral-900/60 border border-white/10 rounded-2xl p-6 backdrop-blur-md shadow-2xl w-full flex flex-col items-center gap-4">
            <div className="w-12 h-12 rounded-xl bg-brand-500/10 border border-brand-500/30 flex items-center justify-center text-2xl text-brand-400">
              📺
            </div>

            <div>
              <h1 className="text-xl font-bold text-white tracking-tight">{screenName}</h1>
              <p className="text-xs text-gray-400 mt-0.5">受控展示大屏已上线，随时接收投屏内容</p>
            </div>

            <div className="flex items-center gap-2 bg-black/40 px-3 py-1.5 rounded-lg border border-white/5 font-mono text-xs text-gray-300">
              <span>屏幕标识:</span>
              <span className="text-brand-300 font-bold">{screenId}</span>
              <button
                onClick={copyScreenId}
                className="ml-2 text-gray-400 hover:text-white transition"
                title="复制屏幕ID"
              >
                📋
              </button>
            </div>

            <div className="text-xs text-gray-500 text-center max-w-sm">
              在手机或电脑打开 Peerdrive 网盘，在文件菜单点击「投屏」，即可实时将图片或视频推送到此屏幕。
            </div>
          </div>
        </div>
      )}

      {/* Bottom Hint */}
      <div className="absolute bottom-4 left-0 right-0 text-center text-[11px] text-gray-600 select-none">
        快捷键: [F] 全屏 | [空格] 播放/暂停 | [C] 清空 | [H] 切换控制栏
      </div>
    </div>
  );
}
