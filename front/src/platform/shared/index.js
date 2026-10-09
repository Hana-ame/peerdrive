// index.js — platform/shared module entry point.
//
// Shared pure functions and utilities across all features and pages:
// - format: display formatting for bytes, transfer speed, and ETA
// - swBridge: service worker registration and stream bridge
// - collectionTree: collection directory tree building and entry normalization

export * from './format.js'
export * from './swBridge.js'
export * from './collectionTree.js'
export * from './storageKeys.js'
