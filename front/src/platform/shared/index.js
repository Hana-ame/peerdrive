// index.js — platform/shared module entry point.
//
// Shared pure functions and single-source-of-truth utilities across all features and pages:
// - format: display formatting for bytes, transfer speed, and ETA
// - swBridge: service worker registration and stream bridge
// - collectionTree: collection directory tree building and entry normalization
// - peerdrive-core: canonical URLs, unified queue/progress hub, and security/permission guards

export * from './format.js'
export * from './swBridge.js'
export * from './collectionTree.js'
export * from './storageKeys.js'
export * from './mime.js'
export * from './errorClassifier.js'
export {
  buildFileUrl,
  buildCollectionUrl,
  parseDirectTarget,
  TransferProgressHub,
  TransportQueue,
  globalProgressHub,
  globalTransportQueue,
  safeJsonParse,
  detectPromptInjection,
  sanitizeUntrustedText,
  sanitizeFilename,
  sanitizeRelativePath,
  safePreviewMime,
  validateSignalOrApiUrl,
  sanitizeCollectionManifest,
  PermissionError,
  evaluateAdminPermission,
} from '../../../../packages/peerdrive-core/src/index.js'
