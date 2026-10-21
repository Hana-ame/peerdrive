// storageKeys.js — Centralized localStorage keys and documentation of credential semantics.
// Resolves #155: Eliminate scattered string literals and clarify auth credential usage.

/**
 * peerdrive_api_base: Base URL for HTTP API and WebSocket admin connection.
 * Default fallback: https://wsl-3000.moonchan.xyz
 */
export const STORAGE_KEY_API_BASE = 'peerdrive_api_base';

/**
 * peerdrive_reg_server_url: Identity / registration server URL for account authentication.
 * Default fallback: https://account.moonchan.xyz
 */
export const STORAGE_KEY_REG_SERVER_URL = 'peerdrive_reg_server_url';

/**
 * peerdrive_auth_token: JWT bearer token issued by the registration server upon login.
 * This is the PRIMARY user account authentication token used across frontend session state.
 */
export const STORAGE_KEY_AUTH_TOKEN = 'peerdrive_auth_token';

/**
 * peerdrive_auth_key: Pre-shared key / token for direct node authorization header.
 * When STORAGE_KEY_AUTH_HEADER_ENABLED is true, this key is attached to administrative requests.
 */
export const STORAGE_KEY_AUTH_KEY = 'peerdrive_auth_key';

/**
 * peerdrive_auth_header_enabled: Flag ('true' | 'false') indicating whether direct node
 * header authorization with STORAGE_KEY_AUTH_KEY is enabled.
 */
export const STORAGE_KEY_AUTH_HEADER_ENABLED = 'peerdrive_auth_header_enabled';

/**
 * peerdrive.panel.v1: PeerJS connection preferences and history cache for the consumer panel.
 * Version suffix '.v1' enables non-breaking migrations when schemas change.
 */
export const STORAGE_KEY_PANEL_PREFS = 'peerdrive.panel.v1';

export const ALL_STORAGE_KEYS = [
  STORAGE_KEY_API_BASE,
  STORAGE_KEY_REG_SERVER_URL,
  STORAGE_KEY_AUTH_TOKEN,
  STORAGE_KEY_AUTH_KEY,
  STORAGE_KEY_AUTH_HEADER_ENABLED,
  STORAGE_KEY_PANEL_PREFS,
];
