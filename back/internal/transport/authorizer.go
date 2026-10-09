package transport

// Authorizer determines whether a peer session is authorized to download a given content hash.
// This is an identity plugin point reserved for ROADMAP Phase 7 (Identity Management).
// In Phase 7, cryptographic identity verification (e.g. signature, auth token) will replace or
// extend the simple peer ID / friend list check.
type Authorizer interface {
	AuthorizeDownload(session Session, hash string, token string) bool
}

// DefaultAuthorizer implements Authorizer using the node's configured ShareGate and session locality.
// Behavior is strictly identical to the pre-Phase 7 gate:
// - Local sessions (IsLocal() == true) always pass;
// - Non-private content passes;
// - Private content requires the peerID to be on the friends list in ShareGate.
type DefaultAuthorizer struct {
	gateFunc func() ShareGate
}

// NewDefaultAuthorizer constructs a DefaultAuthorizer with a dynamic gate lookup function.
func NewDefaultAuthorizer(gateFunc func() ShareGate) *DefaultAuthorizer {
	return &DefaultAuthorizer{gateFunc: gateFunc}
}

// AuthorizeDownload checks whether session is authorized to download hash.
func (a *DefaultAuthorizer) AuthorizeDownload(session Session, hash string, token string) bool {
	if isSelfSession(session) {
		return true
	}
	if a.gateFunc == nil {
		return true
	}
	g := a.gateFunc()
	if g == nil {
		return true
	}
	return g.AllowsDownload(session.ID(), hash, false)
}

// SetAuthorizer injects a custom download authorizer (Phase 7 identity plugin point).
// Passing nil resets to DefaultAuthorizer.
func (s *PeerJSService) SetAuthorizer(a Authorizer) {
	s.shareMu.Lock()
	s.authorizer = a
	s.shareMu.Unlock()
}

// currentAuthorizer returns the active Authorizer, falling back to DefaultAuthorizer.
func (s *PeerJSService) currentAuthorizer() Authorizer {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	if s.authorizer != nil {
		return s.authorizer
	}
	if s.defaultAuthorizer != nil {
		return s.defaultAuthorizer
	}
	return NewDefaultAuthorizer(s.currentShareGate)
}
