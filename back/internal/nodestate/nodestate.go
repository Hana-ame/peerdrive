// Package nodestate holds the runtime identity of a Peerdrive node.
//
// 2026-10-07 cleanup: this package used to carry four package-level fields
// (operator / regURL / authToken / peerID) and five exported functions. Four of
// them had **zero call sites anywhere in the module** and were deleted (see the
// list below); only GetOperator() has real consumers. The package is kept
// (rather than inlined into controller) because controller/anon.go and
// controller/p2p.go both read it and it is nothing more than that read-only
// accessor.
//
// Deleted, and why — all verified by a whole-module symbol search. These are
// plain exported Go functions with no reflection or registry indirection, and
// the only importer of this package in the entire tree is internal/controller:
//
//   - Configure(op, url, token, pid): its only writer caller was NodeRegistrar,
//     deleted along with the libp2p stack (commit a5b090d). Zero call sites since.
//   - SetOperator(username): no caller.
//   - GetPeerID(): no caller.
//   - ReportStats(upload, download): no caller, and it POSTed to
//     `regURL + "/auth/node/stats"` — an endpoint that **does not exist**. The
//     bundled registration server only serves POST /auth/register, POST
//     /auth/login, GET /auth/whoami, GET /auth/list
//     (back/internal/regserver/regserver.go:493-496). So this was a fake
//     implementation that could never have succeeded, while implying a
//     "transfer statistics are reported to the registration server" capability
//     that does not exist. Same failure mode as the three dead config fields
//     removed on 2026-10-04 (see the note at back/internal/config/config.go).
//
// IMPORTANT — operator now has **no writer**: with Configure and SetOperator
// both gone, nothing in the tree can assign it, so GetOperator() always returns
// "". That is the honest current state rather than a regression: those two
// functions had no callers either, so the value was already always "" at
// runtime. Seven call sites read it as the collection owner/operator
// (controller/anon.go:51,97,141,163,239,318 and controller/p2p.go:895); they
// keep working and keep receiving "". Do not add a writer ad hoc — deciding
// what an operator *is* (per-user session vs. node owner) belongs to the
// identity work tracked in doc/modules/auth, which is explicitly last in
// doc/ROADMAP.md. Until that lands, keeping the accessor means that work is a
// one-line change in one place rather than a sweep across seven call sites.
package nodestate

import "sync"

var (
	mu       sync.Mutex
	operator string
)

// GetOperator returns the node operator (empty string = anonymous).
func GetOperator() string {
	mu.Lock()
	defer mu.Unlock()
	return operator
}