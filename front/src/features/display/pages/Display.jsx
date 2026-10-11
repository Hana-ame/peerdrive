import React, { useState, useEffect, useRef } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import * as ws from '../../../ws';
import DisplayHUD from '../components/DisplayHUD';
import DisplayMedia from '../components/DisplayMedia';
import DisplayStandby from '../components/DisplayStandby';

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

    const unbindStatus = ws.onStatus((st) => {
      setConnected(st === 'open');
      if (st === 'open') {
        registerScreen();
      } else {
        setRegistered(false);
      }
    });

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
      <DisplayHUD
        showHud={showHud}
        connected={connected}
        registered={registered}
        screenName={screenName}
        channel={channel}
        media={media}
        onClearMedia={() => setMedia(null)}
        onCopyScreenId={copyScreenId}
        copied={copied}
        screenId={screenId}
        onToggleFullscreen={toggleFullscreen}
      />

      {/* Main Content Area: Media vs Standby */}
      {media ? (
        <DisplayMedia
          media={media}
          mediaSrc={getMediaSrc()}
          videoRef={videoRef}
          audioRef={audioRef}
        />
      ) : (
        <DisplayStandby
          currentTime={currentTime}
          screenName={screenName}
          screenId={screenId}
          onCopyScreenId={copyScreenId}
        />
      )}

      {/* Bottom Hint */}
      <div className="absolute bottom-4 left-0 right-0 text-center text-[11px] text-gray-600 select-none">
        快捷键: [F] 全屏 | [空格] 播放/暂停 | [C] 清空 | [H] 切换控制栏
      </div>
    </div>
  );
}
