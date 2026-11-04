// mime.test.js — unit tests for mime detection and kind classification.
//
// Discovery context (issue #150): files in CollectionBrowser and Drive were previously
// either only treated as images or degraded to raw download. We verify that explicit
// server MIME takes priority over file extension, and missing MIME falls back correctly
// without throwing.

import { describe, it, expect } from 'vitest';
import { extOf, mimeOf, kindOf, MAX_TEXT_PREVIEW_BYTES } from '../src/platform/shared/mime';

describe('mime utilities', () => {
  it('extOf handles various extensions and case', () => {
    expect(extOf('sample.PDF')).toBe('pdf');
    expect(extOf('path/to/archive.tar.gz')).toBe('gz');
    expect(extOf('no_ext')).toBe('');
    expect(extOf('.hidden')).toBe('hidden');
    expect(extOf('')).toBe('');
  });

  it('mimeOf prioritizes server explicit mime over extension', () => {
    // When server returns explicit mime
    expect(mimeOf('unknown_file', 'video/mp4')).toBe('video/mp4');
    expect(mimeOf('picture.jpg', 'image/webp')).toBe('image/webp');

    // Ignores 'folder' or 'unknown' sentinel values
    expect(mimeOf('file.mp4', 'folder')).toBe('video/mp4');
    expect(mimeOf('file.pdf', 'unknown')).toBe('application/pdf');

    // Falls back to extension
    expect(mimeOf('clip.webm')).toBe('video/webm');
    expect(mimeOf('song.flac')).toBe('audio/flac');
    expect(mimeOf('doc.pdf')).toBe('application/pdf');
    expect(mimeOf('script.js')).toBe('text/javascript');
    expect(mimeOf('data.json')).toBe('application/json');
    expect(mimeOf('style.css')).toBe('text/css');
    expect(mimeOf('notes.txt')).toBe('text/plain');
    expect(mimeOf('unknown.xyz123')).toBe('');
  });

  it('kindOf classifies into previewable types', () => {
    // Video
    expect(kindOf('clip.mp4')).toBe('video');
    expect(kindOf('movie.webm')).toBe('video');
    expect(kindOf('anything', 'video/ogg')).toBe('video');

    // Audio
    expect(kindOf('track.mp3')).toBe('audio');
    expect(kindOf('sound.wav')).toBe('audio');

    // PDF
    expect(kindOf('manual.pdf')).toBe('pdf');
    expect(kindOf('doc', 'application/pdf')).toBe('pdf');

    // Text & Code
    expect(kindOf('readme.md')).toBe('text');
    expect(kindOf('config.json')).toBe('text');
    expect(kindOf('main.go')).toBe('text');
    expect(kindOf('script.sh')).toBe('text');

    // Images
    expect(kindOf('photo.png')).toBe('image');
    expect(kindOf('photo.webp')).toBe('image');

    // Unknown/unsupported preview types -> null (degrade to download)
    expect(kindOf('binary.exe')).toBeNull();
    expect(kindOf('archive.zip')).toBeNull();
  });

  it('MAX_TEXT_PREVIEW_BYTES is set to 1MB', () => {
    expect(MAX_TEXT_PREVIEW_BYTES).toBe(1024 * 1024);
  });
});
