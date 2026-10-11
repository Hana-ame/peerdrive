// Utility for Node Banner presets, custom image URLs and deterministic fallback
export const BANNER_PRESETS = [
  {
    id: 'cyberpunk',
    name: 'Cyberpunk Neon',
    gradient: 'linear-gradient(135deg, #0f0c29 0%, #302b63 50%, #24243e 100%)',
    accent: '#00f2fe',
    badge: 'bg-cyan-500/20 text-cyan-300 border-cyan-500/30',
  },
  {
    id: 'aurora',
    name: 'Emerald Aurora',
    gradient: 'linear-gradient(135deg, #093028 0%, #17543f 50%, #237a57 100%)',
    accent: '#48bb78',
    badge: 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30',
  },
  {
    id: 'sunset',
    name: 'Sunset Horizon',
    gradient: 'linear-gradient(135deg, #42275a 0%, #734b6d 50%, #994c6e 100%)',
    accent: '#f6ad55',
    badge: 'bg-amber-500/20 text-amber-300 border-amber-500/30',
  },
  {
    id: 'ocean',
    name: 'Deep Ocean',
    gradient: 'linear-gradient(135deg, #0f2027 0%, #203a43 50%, #2c5364 100%)',
    accent: '#4fd1c5',
    badge: 'bg-teal-500/20 text-teal-300 border-teal-500/30',
  },
  {
    id: 'galaxy',
    name: 'Galaxy Cosmic',
    gradient: 'linear-gradient(135deg, #140b28 0%, #2e1a47 50%, #4a256d 100%)',
    accent: '#b794f4',
    badge: 'bg-purple-500/20 text-purple-300 border-purple-500/30',
  },
  {
    id: 'solar',
    name: 'Solar Flare',
    gradient: 'linear-gradient(135deg, #2b0c0c 0%, #681a1a 50%, #b83b16 100%)',
    accent: '#fc8181',
    badge: 'bg-rose-500/20 text-rose-300 border-rose-500/30',
  },
  {
    id: 'matrix',
    name: 'Neon Matrix',
    gradient: 'linear-gradient(135deg, #031e11 0%, #063e23 50%, #095f36 100%)',
    accent: '#68d391',
    badge: 'bg-green-500/20 text-green-300 border-green-500/30',
  },
  {
    id: 'obsidian',
    name: 'Obsidian Slate',
    gradient: 'linear-gradient(135deg, #141e30 0%, #243b55 100%)',
    accent: '#cbd5e0',
    badge: 'bg-slate-500/20 text-slate-300 border-slate-500/30',
  },
];

const STORAGE_KEY = 'peerdrive_node_banners';

function getAllStoredBanners() {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
  } catch {
    return {};
  }
}

/**
 * Deterministically pick a preset banner based on peerId hash
 */
export function getDefaultPresetBanner(peerId = '') {
  let hash = 0;
  for (let i = 0; i < peerId.length; i++) {
    hash = (hash << 5) - hash + peerId.charCodeAt(i);
    hash |= 0;
  }
  const index = Math.abs(hash) % BANNER_PRESETS.length;
  return BANNER_PRESETS[index];
}

/**
 * Get active banner config for a node (peerId)
 * Returns object with { type: 'preset' | 'url', value: string, preset?: object }
 */
export function getNodeBanner(peerId = '') {
  if (!peerId) return { type: 'preset', value: 'cyberpunk', preset: BANNER_PRESETS[0] };
  const stored = getAllStoredBanners()[peerId];
  if (stored) {
    if (stored.startsWith('http://') || stored.startsWith('https://') || stored.startsWith('data:')) {
      return { type: 'url', value: stored };
    }
    const found = BANNER_PRESETS.find((p) => p.id === stored);
    if (found) return { type: 'preset', value: found.id, preset: found };
  }
  const def = getDefaultPresetBanner(peerId);
  return { type: 'preset', value: def.id, preset: def };
}

/**
 * Save banner selection for a specific peerId
 */
export function setNodeBanner(peerId = '', bannerValue = '') {
  if (!peerId) return;
  const stored = getAllStoredBanners();
  if (!bannerValue) {
    delete stored[peerId];
  } else {
    stored[peerId] = bannerValue.trim();
  }
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(stored));
    window.dispatchEvent(new CustomEvent('peerdrive:banner-change', { detail: { peerId } }));
  } catch {}
}

/**
 * Convert banner config to React CSS style
 */
export function getBannerStyle(bannerConfig) {
  if (!bannerConfig) return { background: BANNER_PRESETS[0].gradient };
  if (bannerConfig.type === 'url') {
    return {
      backgroundImage: `url(${bannerConfig.value})`,
      backgroundSize: 'cover',
      backgroundPosition: 'center',
    };
  }
  const p = bannerConfig.preset || BANNER_PRESETS.find((x) => x.id === bannerConfig.value) || BANNER_PRESETS[0];
  return { background: p.gradient };
}
