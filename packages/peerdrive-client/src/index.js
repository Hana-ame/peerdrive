// index.js — public entry point of peerdrive-client.
//
// Two things: export both the pure functions of the frame protocol (protocol.js)
// and the client state machine (client.js). Using `export *` instead of listing
// individually is to avoid editing two places when adding a new protocol constant
// (most new things in the protocol implementation should be visible to users —
// they need it for UI branching).

export * from './protocol.js'
export * from './client.js'
