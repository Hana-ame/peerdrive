import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  STORAGE_KEY_NODE_PSK,
  STORAGE_KEY_AUTH_TOKEN,
} from '../src/platform/shared/storageKeys';
import {
  readNodePSK,
  saveNodePSK,
  buildAuthHeaders,
  withPSKQuery,
  shouldPromptOnUnauthorized,
  requestCredential,
} from '../src/platform/shared/nodePSK';

// PSKGate 的面板侧：读/存 node PSK、构造请求头、给无法带头的媒体 URL 附查询参数、
// 401 后的凭据输入框。后端对应 internal/router/auth_middleware.go 的 PSKGate：
// X-PSK 头或 ?psk= 查询，二者都有；管理员令牌也放行。
// 这些用例钉住的是「两个呈现路径都走通」和「并发 401 只弹一次」。

const resetAll = () => {
  localStorage.clear();
  sessionStorage.clear();
};

// happy-dom 没有 window.prompt：stubGlobal 到 globalThis（happy-dom 里 window === globalThis），
// 这样 module 里的 typeof window.prompt === 'function' 判定才成立。
const stubPrompt = (value) => {
  const fn = vi.fn().mockReturnValue(value);
  vi.stubGlobal('prompt', fn);
  return fn;
};

beforeEach(resetAll);
afterEach(() => {
  resetAll();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('readNodePSK / saveNodePSK', () => {
  it('未存过返回空串；存过原样返回', () => {
    expect(readNodePSK()).toBe('');
    saveNodePSK('s3cret');
    expect(readNodePSK()).toBe('s3cret');
  });

  it('存的时候 trim，空白不保存（留空＝门禁关，这是历史默认）', () => {
    saveNodePSK('  key  ');
    expect(readNodePSK()).toBe('key');
    saveNodePSK('   ');
    expect(readNodePSK()).toBe('');
    expect(localStorage.getItem(STORAGE_KEY_NODE_PSK)).toBeNull();
  });
});

describe('buildAuthHeaders', () => {
  it('什么都没有时返回空对象 —— 不带头等于「门禁没开」', () => {
    expect(buildAuthHeaders()).toEqual({});
  });

  it('只存了 PSK → 只发 X-PSK（PSKGate 的首选呈现方式）', () => {
    saveNodePSK('node-psk-1');
    expect(buildAuthHeaders()).toEqual({ 'X-PSK': 'node-psk-1' });
  });

  it('PSK + 注册服务器令牌 → 两个都发；Bearer 是同一个操作者的另一把钥匙', () => {
    saveNodePSK('node-psk-1');
    localStorage.setItem(STORAGE_KEY_AUTH_TOKEN, 'jwt-abc');
    expect(buildAuthHeaders()).toEqual({
      'X-PSK': 'node-psk-1',
      Authorization: 'Bearer jwt-abc',
    });
  });

  it('sessionStorage 里的令牌优先于 localStorage（与 transport 层 readToken 的顺序一致）', () => {
    localStorage.setItem(STORAGE_KEY_AUTH_TOKEN, 'local');
    sessionStorage.setItem(STORAGE_KEY_AUTH_TOKEN, 'session');
    expect(buildAuthHeaders()).toEqual({ Authorization: 'Bearer session' });
  });

  it('extra 头合进来，自己的认证头不被覆盖', () => {
    saveNodePSK('k');
    localStorage.setItem(STORAGE_KEY_AUTH_TOKEN, 't');
    expect(buildAuthHeaders({ 'X-Extra': '1', 'X-PSK': 'should-not-win' })).toEqual({
      'X-Extra': '1',
      'X-PSK': 'k',
      Authorization: 'Bearer t',
    });
  });
});

describe('withPSKQuery', () => {
  it('没有 PSK → URL 原样返回（媒体请求不加参数，历史行为不变）', () => {
    expect(withPSKQuery('/sha256sum/abc/1.txt')).toBe('/sha256sum/abc/1.txt');
  });

  it('追加 ?psk=（<video>/<img> 加不了自定义请求头，只能走查询参数）', () => {
    saveNodePSK('video-psk');
    expect(withPSKQuery('/sha256sum/abc/1.txt')).toBe('/sha256sum/abc/1.txt?psk=video-psk');
  });

  it('已有其他查询参数 → 用 &', () => {
    saveNodePSK('k');
    expect(withPSKQuery('/f/abc?meta=1')).toBe('/f/abc?meta=1&psk=k');
  });

  it('已经带 psk 的 URL 不覆盖（不猜调用方意图）', () => {
    saveNodePSK('new');
    expect(withPSKQuery('/f/abc?psk=old')).toBe('/f/abc?psk=old');
  });

  it('相对路径也能处理（apiBase 为空时面板直接挂在同一台 node 上）', () => {
    saveNodePSK('k');
    expect(withPSKQuery('/sha256sum/h/n.mp4')).toBe('/sha256sum/h/n.mp4?psk=k');
  });

  it('绝对 URL 不改变 scheme/host', () => {
    saveNodePSK('k');
    expect(withPSKQuery('https://n.example/x?y=1')).toBe('https://n.example/x?y=1&psk=k');
  });

  it('有 #fragment 时插在 fragment 之前（插在后面会被当 hash 的一部分）', () => {
    saveNodePSK('k');
    expect(withPSKQuery('/f/abc?x=1#frag')).toBe('/f/abc?x=1&psk=k#frag');
    expect(withPSKQuery('/f/abc#frag')).toBe('/f/abc?psk=k#frag');
  });

  it('空 URL / 非字符串 → 原样返回，不抛错', () => {
    saveNodePSK('k');
    expect(withPSKQuery('')).toBe('');
    expect(withPSKQuery(null)).toBe(null);
  });
});

describe('shouldPromptOnUnauthorized', () => {
  it('没存过 PSK → 弹框（第一次接触一台设了门禁的 node）', () => {
    expect(shouldPromptOnUnauthorized()).toBe(true);
  });

  it('存了 PSK 却仍然 401 → 不弹框，去 Settings 改（弹了也只会重复失败）', () => {
    saveNodePSK('wrong');
    expect(shouldPromptOnUnauthorized()).toBe(false);
  });

  it('清空后又回到要弹框的状态', () => {
    saveNodePSK('k');
    expect(shouldPromptOnUnauthorized()).toBe(false);
    saveNodePSK('');
    expect(shouldPromptOnUnauthorized()).toBe(true);
  });
});

describe('requestCredential', () => {
  it('用户输入 → resolve 该值并持久化', async () => {
    stubPrompt('typed-psk');
    await expect(requestCredential('test')).resolves.toBe('typed-psk');
    expect(readNodePSK()).toBe('typed-psk');
  });

  it('取消（null）→ resolve null 且不写存储', async () => {
    stubPrompt(null);
    await expect(requestCredential('test')).resolves.toBeNull();
    expect(readNodePSK()).toBe('');
  });

  it('空白输入 → resolve null', async () => {
    stubPrompt('   ');
    await expect(requestCredential('test')).resolves.toBeNull();
    expect(readNodePSK()).toBe('');
  });

  it('输入会 trim', async () => {
    stubPrompt('  k1  ');
    await expect(requestCredential('test')).resolves.toBe('k1');
    expect(readNodePSK()).toBe('k1');
  });

  it('提示文案带上了原因，且提示「取消＝本次放弃」', () => {
    const promptSpy = stubPrompt(null);
    requestCredential('这台 node 要求 PSK');
    const [msg] = promptSpy.mock.calls[0];
    expect(msg).toContain('这台 node 要求 PSK');
    expect(msg).toContain('取消');
  });

  it('没传 reason 时也有兜底文案', () => {
    const promptSpy = stubPrompt(null);
    requestCredential();
    expect(promptSpy.mock.calls[0][0]).toContain('访问被拒绝');
  });

  it('已经存了 PSK 时仍然弹框 —— 允许当场纠正输错的钥匙', async () => {
    saveNodePSK('wrong');
    stubPrompt('right');
    await expect(requestCredential('test')).resolves.toBe('right');
    expect(readNodePSK()).toBe('right');
  });

  it('环境里没有 window.prompt（非浏览器）→ 静默 resolve null，不抛错', async () => {
    const savedPrompt = globalThis.prompt;
    vi.stubGlobal('prompt', undefined);
    expect(() => requestCredential('x')).not.toThrow();
    await expect(requestCredential('x')).resolves.toBeNull();
    vi.stubGlobal('prompt', savedPrompt);
  });
});
