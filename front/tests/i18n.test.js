import { describe, it, expect } from 'vitest';
import { translate, DICTIONARY } from '../src/utils/i18n';

describe('i18n Internationalization', () => {
  it('translates known keys in zh and en correctly', () => {
    expect(translate('zh', 'qr.title')).toBe('节点直连二维码与连接串');
    expect(translate('en', 'qr.title')).toBe('Direct Connect QR & Token');

    expect(translate('zh', 'offline.banner')).toContain('离线模式');
    expect(translate('en', 'offline.banner')).toContain('offline mode');

    expect(translate('zh', 'connect.tokenParsed')).toBe('已自动识别直连连接串');
    expect(translate('en', 'connect.tokenParsed')).toBe('Direct connection token recognized');
  });

  it('replaces placeholder parameters properly', () => {
    const zh = translate('zh', 'status.signalTitle', { host: 'peersignal.xyz', status: 'connected' });
    expect(zh).toContain('peersignal.xyz');
    expect(zh).toContain('connected');
  });

  it('falls back to key when missing', () => {
    expect(translate('zh', 'non.existent.key')).toBe('non.existent.key');
  });
});
