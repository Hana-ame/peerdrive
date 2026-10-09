// tracker_auth.go — JWT auth functions for the BitTorrent HTTP tracker.
//
// These adapt the regserver's JWT verification (verifyToken) to the tracker's
// auth interfaces:
//
//   - jwtBanAuth: gate for /tracker/bans management. Accepts any valid
//     Bearer JWT (not just admin tokens) — the same protocol as
//     authRequired. When no JWT_SECRET is configured (db not opened),
//     returns a function that always denies.
//
//   - jwtVerifyUser: adapter for the tracker's VerifyUser callback.
//     Extracts the username from a Bearer JWT, so announce requests with
//     a valid token are automatically associated with the user's identity
//     for user-level bans.

package regserver

import (
	"net/http"
	"strings"
)

// jwtBanAuth returns an auth function for tracker ban management that
// accepts any valid Bearer JWT token. Mirrors authRequired's protocol:
// only "Bearer <token>" (space-separated, case-insensitive scheme).
func (s *Server) jwtBanAuth() func(*http.Request) bool {
	return func(r *http.Request) bool {
		h := r.Header.Get("Authorization")
		if h == "" {
			return false
		}
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			return false
		}
		_, _, err := s.verifyToken(parts[1])
		return err == nil
	}
}

// jwtVerifyUser returns a callback for the tracker's user→peer_id mapping.
// It verifies a Bearer JWT token and returns the username on success.
func (s *Server) jwtVerifyUser() func(string) (string, bool) {
	return func(token string) (string, bool) {
		username, _, err := s.verifyToken(token)
		if err != nil {
			return "", false
		}
		return username, true
	}
}
