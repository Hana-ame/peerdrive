package main

// security_status.go — security status summary at startup (2026-10-04).
//
// Why this file exists: the node ships with several switches that are **off by default**,
// so a default-config node silently accepts connections from anyone. There are currently
// **no prompts at startup** for any of them — the operator has no way to learn that the node
// is open apart from knowing how the project is implemented. This file collects those
// switch states into a single summary, printed after the router is assembled, making
// "where exactly is this node open" something you can see directly.
//
// Why not just hard-fail instead:
//   - Forcing PEERDRIVE_PSK to be set would make every existing deployment fail to start
//     (the env block in doc/tutorial/01-run-and-connect.md:101-112 has no PSK, and
//     panel/app.js:723 does not present a PSK by default, so all public-panel consumers
//     would be locked out) — see the Round 1 rationale in the security fix plan;
//   - CI has zero coverage of the PSK path (`grep -rn PEERDRIVE_PSK .github/` is an empty
//     hit, and e2e.yml's env block has no PSK either), so changing this has no test net.
// First change visibility, then add gates.
//
// Boundary: this file is **read-only** — it only reads cfg and calls share.Enabled().
// It does not modify any config item or change any default.

import (
	"peerdrive/internal/config"
	"peerdrive/internal/service"

	"peerdrive/internal/log"
)

// securityFinding is a single "open by default" item.
// Level semantics (for logging):
//   - "warn": really needs operator attention (this node is reachable by strangers);
//   - "info": just an informational statement (closed by default, the operator configured it).
type securityFinding struct {
	Level   string
	Title   string
	Detail  string
	Remedy  string
}

// collectSecurityFindings reads config and share state and yields all conclusions.
// share may be nil (when PeerJS is disabled this path isn't reached) —
// treat "sharing service unavailable" as not enabled.
func collectSecurityFindings(cfg *config.Config, share *service.NodeShare) []securityFinding {
	var out []securityFinding

	// 1. Inbound P2P gate: the PSK's only real gate. transport/psk.go:60 pskEnabled() is
	//    literally cfg.PeerPSK != "", so an empty value means open.
	if cfg.PeerPSK == "" {
		out = append(out, securityFinding{
			Level:  "warn",
			Title:  "inbound P2P has no gate (PEERDRIVE_PSK empty)",
			Detail: "once a peer id is known, anyone can connect and pull share content; an empty PSK means open mode",
			Remedy: "set PEERDRIVE_PSK, or bind PEERDRIVE_HOST=127.0.0.1 to limit the admin surface to this machine",
		})
	}

	// 2. HTTP admin surface: router/auth_middleware.go:28 authDisabled() is
	//    literally regServerURL == "", i.e. an empty RegistrationServer means auth is disabled.
	if cfg.RegistrationServer == "" {
		out = append(out, securityFinding{
			Level:  "warn",
			Title:  "HTTP admin surface has no auth (PEERDRIVE_REG_SERVER empty)",
			Detail: "list/delete files, change sharing scope, read Swagger API docs — all open without a credential",
			Remedy: "set PEERDRIVE_REG_SERVER, or bind PEERDRIVE_HOST=127.0.0.1",
		})
	}

	// 3. Public roster: on by default; the node's online status, peer id, and
	//    which collections it follows are announced to the public signaling server, which accepts
	//    dial-backs from strangers.
	if cfg.DiscoverPresence {
		out = append(out, securityFinding{
			Level:  "warn",
			Title:  "node is present in the public roster (PEERDRIVE_DISCOVER_PRESENCE=true)",
			Detail: "peer id and sharing summary are announced to the signaling server, and strangers can call back",
			Remedy: "set PEERDRIVE_DISCOVER_PRESENCE=false, or set PEERDRIVE_PSK to add another layer of gating",
		})
	}

	// 4. Swagger: on by default; router.go:398 is enabled as long as !cfg.DisableSwagger.
	//    Publicly announces the complete endpoint and parameter structure.
	if !cfg.DisableSwagger {
		out = append(out, securityFinding{
			Level:  "warn",
			Title:  "Swagger API docs are public (PEERDRIVE_SWAGGER not off)",
			Detail: "/swagger/index.html exposes the endpoint and parameter structure of all 105+ endpoints without a credential",
			Remedy: "set PEERDRIVE_SWAGGER=off",
		})
	}

	// 5. External sharing: on by default = closed (service/nodeshare.go:309).
	//    This one is "closed", so it's informational — but the operator often thinks
	//    "why can't others see my files" and needs to know the switch exists.
	if share == nil || !share.Enabled() {
		out = append(out, securityFinding{
			Level:  "info",
			Title:  "external sharing not enabled (PEERDRIVE_SHARE_ENABLE not true)",
			Detail: "this is the safer default; other parties see an empty manifest when connecting",
			Remedy: "enable it plus PEERDRIVE_SHARE_DIRS if you want to provide content to the outside",
		})
	}

	return out
}

// logSecuritySummary prints the security status summary.
// Always prints (even with zero findings, it prints "everything closed" explicitly) —
// the operator needs to learn "checked, all closed" from the log,
// not "no output means probably fine".
func logSecuritySummary(cfg *config.Config, share *service.NodeShare) {
	findings := collectSecurityFindings(cfg, share)

	if len(findings) == 0 {
		log.LogInfo("security: all gates closed (inbound PSK / admin-surface auth / public roster / Swagger)")
		return
	}

	warnCount := 0
	for _, f := range findings {
		if f.Level == "warn" {
			warnCount++
		}
	}

	log.LogWarn("security: %d open item(s) need operator attention, %d informational item(s)", warnCount, len(findings)-warnCount)
	for _, f := range findings {
		switch f.Level {
		case "warn":
			log.LogWarn("security: [open] %s — %s (fix: %s)", f.Title, f.Detail, f.Remedy)
		default:
			log.LogInfo("security: [info] %s — %s", f.Title, f.Detail)
		}
	}
}
