// index.js — public entry of peerdrive-client.
//
// Two things: export both the pure functions of the frame protocol (protocol.js)
// and the client state machine (client.js).
// Using `export *` instead of listing individually is to avoid having to modify
// two places when a new protocol constant is added (most new things in the
// protocol implementation should be visible to consumers — they need it for
// UI decisions).

export * from './protocol.js'
export * from './client.js'