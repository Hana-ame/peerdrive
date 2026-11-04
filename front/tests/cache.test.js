import { describe, it, expect, vi } from 'vitest';
import { LRUCache } from '../src/lib/cache';

describe('LRUCache', () => {
  it('stores and retrieves values', () => {
    const cache = new LRUCache({ max: 3 });
    cache.set('a', 1);
    cache.set('b', 2);
    expect(cache.get('a')).toBe(1);
    expect(cache.get('b')).toBe(2);
    expect(cache.has('a')).toBe(true);
    expect(cache.has('c')).toBe(false);
  });

  it('evicts least recently used item when exceeding max count', () => {
    const cache = new LRUCache({ max: 2 });
    cache.set('a', 1);
    cache.set('b', 2);
    cache.get('a'); // 'a' is now most recently used, 'b' is oldest
    cache.set('c', 3); // should evict 'b'

    expect(cache.has('b')).toBe(false);
    expect(cache.has('a')).toBe(true);
    expect(cache.has('c')).toBe(true);
  });

  it('evicts items when exceeding maxBytes budget', () => {
    const evicted = [];
    const cache = new LRUCache({
      max: 10,
      maxBytes: 100,
      onEvict: (key, val) => evicted.push({ key, val }),
    });

    cache.set('k1', 'val1', 60);
    cache.set('k2', 'val2', 50); // total 110 > 100 → evicts k1 (60 bytes)

    expect(cache.has('k1')).toBe(false);
    expect(cache.has('k2')).toBe(true);
    expect(evicted).toEqual([{ key: 'k1', val: 'val1' }]);
  });

  it('triggers onEvict callback when deleting or clearing', () => {
    const onEvict = vi.fn();
    const cache = new LRUCache({ max: 5, onEvict });
    cache.set('k1', 'v1');
    cache.set('k2', 'v2');
    cache.delete('k1');
    expect(onEvict).toHaveBeenCalledWith('k1', 'v1');

    cache.clear();
    expect(onEvict).toHaveBeenCalledWith('k2', 'v2');
  });
});
