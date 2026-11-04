// useDriveCast.js — Remote Display Cast state & actions hook (Issue #243).
// Extracted from Drive.jsx per AGENTS.md modularization rules.
import { useState } from 'react';
import * as ws from '../../../platform/transport-ws';
import { getNodeSession } from '../../../lib/nodeSession';

export function useDriveCast({ peerSession, setBusy }) {
  const [castFile, setCastFile] = useState(null);
  const [screens, setScreens] = useState([]);
  const [targetScreenId, setTargetScreenId] = useState('');
  const [targetChannel, setTargetChannel] = useState('default');
  const [castStatus, setCastStatus] = useState('');

  const openCastModal = async (f) => {
    setCastFile(f);
    setCastStatus('');
    if (peerSession?.client || getNodeSession()?.client) {
      setScreens([]);
      return;
    }
    try {
      const res = await ws.admin('GET', '/display/screens');
      const list = Array.isArray(res) ? res : [];
      setScreens(list);
      if (list.length > 0) {
        setTargetScreenId(list[0].id);
        setTargetChannel(list[0].channel || 'default');
      } else {
        setTargetScreenId('');
        setTargetChannel('default');
      }
    } catch {
      setScreens([]);
    }
  };

  const handleCast = async () => {
    if (!castFile) return;
    setBusy(true);
    setCastStatus('Casting to screen...');
    try {
      const ext = (castFile.filename || '').split('.').pop().toLowerCase();
      let mediaType = 'image';
      if (['mp4', 'mkv', 'webm', 'mov', 'avi'].includes(ext)) mediaType = 'video';
      else if (['mp3', 'wav', 'flac', 'ogg', 'aac'].includes(ext)) mediaType = 'audio';

      await ws.admin('POST', '/display/cast', {
        targetSessionId: targetScreenId,
        channel: targetChannel,
        mediaType,
        hash: castFile.hash,
        title: castFile.filename,
        autoplay: true,
      });
      setCastStatus('Cast successful!');
    } catch (ex) {
      setCastStatus('Cast failed: ' + (ex?.message || String(ex)));
    } finally {
      setBusy(false);
    }
  };

  const handleCastControl = async (action) => {
    try {
      if (action === 'clear') {
        await ws.admin('POST', '/display/clear', {
          targetSessionId: targetScreenId,
          channel: targetChannel,
        });
        setCastStatus('Screen cleared');
      } else {
        await ws.admin('POST', '/display/control', {
          targetSessionId: targetScreenId,
          channel: targetChannel,
          controlAction: action,
        });
        setCastStatus(`Control ${action} sent`);
      }
    } catch (ex) {
      setCastStatus('Control failed: ' + (ex?.message || String(ex)));
    }
  };

  return {
    castFile,
    setCastFile,
    screens,
    targetScreenId,
    setTargetScreenId,
    targetChannel,
    setTargetChannel,
    castStatus,
    openCastModal,
    handleCast,
    handleCastControl,
  };
}
