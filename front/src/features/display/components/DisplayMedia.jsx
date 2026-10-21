import React from 'react';

export default function DisplayMedia({
  media,
  mediaSrc,
  videoRef,
  audioRef,
}) {
  if (!media) return null;

  return (
    <div className="relative w-full h-full flex items-center justify-center">
      {media.mediaType === 'image' && (
        <div className="relative w-full h-full flex flex-col items-center justify-center p-4">
          <img
            src={mediaSrc}
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
            src={mediaSrc}
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
            src={mediaSrc}
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
  );
}
