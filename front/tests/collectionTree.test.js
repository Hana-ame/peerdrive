// collectionTree.js unit tests — the pure tree-building logic behind the
// collection folder view.
//
// Discovery context (2026-10, collection frontend batch): the folder view's
// correctness lives in entry normalization + tree building, not in the DOM.
// Three formats must coexist while the backend PR lands (path/sha/preview new
// format, hash legacy, providers current), and path conflicts ("a/b" file vs
// "a/b/c" deeper entry) are exactly the "looks right by eyeball, wrong at the
// boundary" cases this file pins. The rules are: dedupe by path (first wins),
// and dirs always win name conflicts regardless of entry order.

import { describe, it, expect } from 'vitest'
import {
  normalizeEntry, splitPath, basename, buildTree, descend, entrySha, previewSha,
} from '../src/features/collection/lib/collectionTree'

const SHA = (c) => c.repeat(64) // 64-hex fake sha

describe('normalizeEntry', () => {
  it('new format (path/sha/preview) passes through', () => {
    const e = normalizeEntry({ path: 'photos/a.jpg', sha: SHA('a'), preview: SHA('b'), size: 1024, mime_type: 'image/jpeg' })
    expect(e).toEqual({ path: 'photos/a.jpg', sha: SHA('a'), preview: SHA('b'), size: 1024, mime: 'image/jpeg' })
  })
  it('empty/absent preview stays empty (no preview → placeholder)', () => {
    expect(normalizeEntry({ path: 'a.txt', sha: SHA('a') }).preview).toBe('')
    expect(normalizeEntry({ path: 'a.txt', sha: SHA('a'), preview: '' }).preview).toBe('')
  })
  it('legacy hash and current providers formats resolve sha', () => {
    expect(normalizeEntry({ path: 'a.txt', hash: SHA('h') }).sha).toBe(SHA('h'))
    expect(normalizeEntry({ path: 'a.txt', providers: [{ type: 'sha256', value: SHA('p'), mime_type: 'text/plain' }] }).sha).toBe(SHA('p'))
    expect(normalizeEntry({ path: 'a.txt', providers: [{ type: 'sha256', value: SHA('p'), mime_type: 'text/plain' }] }).mime).toBe('text/plain')
  })
  it('entry without a resolvable sha is kept (structure visible, not actionable)', () => {
    expect(normalizeEntry({ path: 'x' })).toEqual({ path: 'x', sha: '', preview: '', size: null, mime: '' })
  })
  it('missing/invalid path → null', () => {
    expect(normalizeEntry(null)).toBeNull()
    expect(normalizeEntry({})).toBeNull()
    expect(normalizeEntry({ path: '', sha: SHA('a') })).toBeNull()
    expect(normalizeEntry({ path: '   ', sha: SHA('a') })).toBeNull()
  })
})

describe('splitPath', () => {
  it('normalizes separators and edges', () => {
    expect(splitPath('/a/b/c.txt')).toEqual(['a', 'b', 'c.txt'])
    expect(splitPath('a/b/c.txt')).toEqual(['a', 'b', 'c.txt'])
    expect(splitPath('a\\b\\c.txt')).toEqual(['a', 'b', 'c.txt'])
    expect(splitPath('/a//b/')).toEqual(['a', 'b'])
    expect(splitPath('')).toEqual([])
    expect(splitPath('/')).toEqual([])
  })
})

describe('basename', () => {
  it('last segment, falls back to the path itself', () => {
    expect(basename('/a/b/c.txt')).toBe('c.txt')
    expect(basename('c.txt')).toBe('c.txt')
    expect(basename('')).toBe('')
  })
})

describe('buildTree', () => {
  it('nests entries by path segments, dirs before files, alphabetical', () => {
    const root = buildTree([
      { path: 'a/b.txt', sha: SHA('1') },
      { path: 'a/c/d.txt', sha: SHA('2') },
      { path: 'root.txt', sha: SHA('3') },
      { path: 'a/alpha.txt', sha: SHA('4') },
    ])
    expect(root.children.map((c) => c.name)).toEqual(['a', 'root.txt'])
    const a = root.children[0]
    expect(a.type).toBe('dir')
    // dirs (c) before files (alpha.txt, b.txt); files alphabetical
    expect(a.children.map((c) => `${c.type}:${c.name}`)).toEqual(['dir:c', 'file:alpha.txt', 'file:b.txt'])
    expect(a.children[0].children[0].name).toBe('d.txt')
    expect(root.children[1].entry.sha).toBe(SHA('3'))
  })

  it('dedupes identical paths (first wins)', () => {
    const root = buildTree([
      { path: 'a/b.txt', sha: SHA('1') },
      { path: 'a/b.txt', sha: SHA('2') },
    ])
    const a = root.children[0]
    expect(a.children).toHaveLength(1)
    expect(a.children[0].entry.sha).toBe(SHA('1'))
  })

  it('dir always wins a file/dir name conflict, regardless of entry order', () => {
    // file-first: "a/b" file then deeper "a/b/c" — the file yields to the dir
    let root = buildTree([
      { path: 'a/b.txt', sha: SHA('1') },
      { path: 'a/b.txt/c.txt', sha: SHA('2') },
    ])
    const a = root.children[0]
    expect(a.children.map((c) => `${c.type}:${c.name}`)).toEqual(['dir:b.txt'])
    expect(a.children[0].children[0].name).toBe('c.txt')

    // dir-first: deeper "a/b/c" then file "a/b" — same result
    root = buildTree([
      { path: 'a/b.txt/c.txt', sha: SHA('2') },
      { path: 'a/b.txt', sha: SHA('1') },
    ])
    expect(root.children[0].children.map((c) => `${c.type}:${c.name}`)).toEqual(['dir:b.txt'])
  })

  it('invalid entries are skipped; empty input → empty root', () => {
    expect(buildTree([null, {}, { path: '', sha: SHA('1') }]).children).toEqual([])
    expect(buildTree([]).children).toEqual([])
  })

  it('root-level file and nested dir coexist', () => {
    const root = buildTree([
      { path: 'a.txt', sha: SHA('1') },
      { path: 'a/b.txt', sha: SHA('2') },
    ])
    expect(root.children.map((c) => `${c.type}:${c.name}`)).toEqual(['dir:a', 'file:a.txt'])
  })
})

describe('descend', () => {
  it('walks dir segments; null when a segment is missing', () => {
    const root = buildTree([
      { path: 'a/b/c.txt', sha: SHA('1') },
      { path: 'x.txt', sha: SHA('2') },
    ])
    expect(descend(root, ['a', 'b']).children[0].name).toBe('c.txt')
    expect(descend(root, ['a', 'nope'])).toBeNull()
    expect(descend(root, [])).toBe(root)
  })
})

describe('entrySha / previewSha', () => {
  it('resolve across formats', () => {
    expect(entrySha({ path: 'a', sha: SHA('s') })).toBe(SHA('s'))
    expect(entrySha({ path: 'a', hash: SHA('h') })).toBe(SHA('h'))
    expect(entrySha({ path: 'a', providers: [{ type: 'sha256', value: SHA('p') }] })).toBe(SHA('p'))
    expect(entrySha({ path: 'a' })).toBe('')
    expect(previewSha({ path: 'a', preview: SHA('p') })).toBe(SHA('p'))
    expect(previewSha({ path: 'a', preview: '' })).toBe('')
    expect(previewSha({ path: 'a' })).toBe('')
  })
})
