// Package httpd is the HTTP listening/serving shell for peerdrive: building the
// listen address, loading TLS, running one http.Server, handling the shutdown
// signals and draining gracefully.
//
// Why this package exists
// -----------------------
// Before it, peerdrive carried four independent implementations of the same
// "open a port and serve a handler on it" sequence, each with slightly
// different details:
//
//   - serverapp.RunHTTP            http.Server + SIGINT/SIGTERM + 15s
//     read-header + 20s graceful drain
//   - services.ServeHTTP           TLS via tls.LoadX509KeyPair, no signals
//   - regserver.Server.Serve       TLS via http.ListenAndServeTLS(cert, key)
//   - signalserver/cmd/peersignal  TLS via http.ListenAndServeTLS(addr, ...)
//
// Three of them now share one implementation. The fourth is in its own module
// (github.com/Hana-ame/go-peerserver, back/signalserver) and cannot import
// internal packages of the main module, so it keeps its own copy by necessity —
// its route list is pinned against this repo by signals_compat_test.go instead.
//
// What this package deliberately does NOT own
// -------------------------------------------
// Route registration, business handlers, the gin middleware chain and config
// parsing stay where they are (router, controller, services, config). httpd
// only opens the door and hands traffic to a handler that is already assembled.
// The split is "who owns the socket", not "who owns the routes".
package httpd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"peerdrive/internal/log"
)

const defaultLogPrefix = "httpd"

const (
	// DefaultReadHeaderTimeout is the Slowloris defence peerdrive has always
	// used on the main service surface (pre-split: serverapp.RunHTTP hardcoded
	// 15*time.Second).
	//
	// It is deliberately NOT applied automatically: the signal and registration
	// surfaces have always served with ReadHeaderTimeout zero, and this split
	// is required to keep timeout values identical. A caller that wants it sets
	// Config.ReadHeaderTimeout explicitly.
	DefaultReadHeaderTimeout = 15 * time.Second

	// DefaultShutdownTimeout bounds the graceful drain. When it expires the
	// listener is closed hard and in-flight requests are dropped — the
	// documented behaviour of a bounded shutdown, not a failure.
	DefaultShutdownTimeout = 20 * time.Second
)

// Config describes one listening surface. The zero value is not usable: Addr
// and a handler are both required.
type Config struct {
	// Addr is the listen address, "host:port". An empty host or ":port" binds
	// every interface.
	Addr string

	// CertFile and KeyFile enable TLS, and only together: one without the
	// other is rejected rather than silently downgraded to plain HTTP.
	CertFile string
	KeyFile  string

	// ReadHeaderTimeout is http.Server.ReadHeaderTimeout. Zero keeps
	// http.Server's own zero value (no read-header timeout) — the behaviour of
	// the signal and registration surfaces before this split.
	ReadHeaderTimeout time.Duration

	// ShutdownTimeout bounds Server.Shutdown. Zero falls back to
	// DefaultShutdownTimeout.
	ShutdownTimeout time.Duration

	// LogPrefix is prepended to every log line this server emits. Empty means
	// "httpd". Kept configurable so the surfaces that previously logged
	// "main: starting HTTP server on ..." keep logging exactly that.
	LogPrefix string
}

// Server owns one http.Server plus the TLS material it serves with.
//
// A Server is safe to construct once and Serve at most once: http.Server.Serve
// is not re-entrant on a closed listener. Build one Server per listening
// surface.
type Server struct {
	cfg     Config
	prefix  string
	tlsCfg  *tls.Config
	httpSrv *http.Server

	mu  sync.Mutex
	lis net.Listener
}

// New builds a Server with its handler injected at construction.
//
// Handler injection is a constructor argument rather than a setter because the
// handler must exist before the server can serve, and the four call sites all
// already have it at that point. It also means a Server can never be built in
// a state where it would serve a nil handler.
func New(cfg Config, h http.Handler) (*Server, error) {
	if h == nil {
		return nil, errors.New("httpd: handler is required")
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return nil, errors.New("httpd: Addr is required")
	}

	var tlsCfg *tls.Config
	if cfg.CertFile != "" || cfg.KeyFile != "" {
		if cfg.CertFile == "" || cfg.KeyFile == "" {
			// A half-supplied TLS pair is the classic typo. Falling through to
			// plain HTTP here would ship a deployment that believes it serves
			// HTTPS; failing loudly at startup is cheaper than debugging a
			// browser that silently downgrades.
			return nil, fmt.Errorf("httpd: TLS requires both a cert and a key (cert=%q key=%q)",
				cfg.CertFile, cfg.KeyFile)
		}
		pair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("httpd: load TLS keypair: %w", err)
		}
		tlsCfg = &tls.Config{Certificates: []tls.Certificate{pair}}
	}

	st := cfg.ShutdownTimeout
	if st <= 0 {
		st = DefaultShutdownTimeout
	}
	cfg.ShutdownTimeout = st
	prefix := cfg.LogPrefix
	if prefix == "" {
		prefix = defaultLogPrefix
	}

	return &Server{
		cfg:    cfg,
		prefix: prefix,
		tlsCfg: tlsCfg,
		httpSrv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           h,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			TLSConfig:         tlsCfg,
		},
	}, nil
}

// Addr is the configured listen address.
func (s *Server) Addr() string { return s.cfg.Addr }

// ListenerAddr is the address actually bound, so ":0" callers learn which port
// they got. It is nil until Serve has bound.
func (s *Server) ListenerAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lis == nil {
		return nil
	}
	return s.lis.Addr()
}

// TLS reports whether this server serves HTTPS.
func (s *Server) TLS() bool { return s.tlsCfg != nil }

func (s *Server) logInfo(format string, args ...any)  { log.LogInfo(s.prefix+": "+format, args...) }
func (s *Server) logWarn(format string, args ...any)  { log.LogWarn(s.prefix+": "+format, args...) }
func (s *Server) logError(format string, args ...any) { log.LogError(s.prefix+": "+format, args...) }

// Serve runs the server and blocks until it stops.
//
// Two ways out, and both end in a graceful drain:
//
//   - the listener fails (address in use, permission, bad port) — the error is
//     returned;
//   - ctx is cancelled — nil is returned.
//
// Shutdown is attempted on both paths. That matches the pre-split RunHTTP,
// which drained even after a listen failure: callers relied on the listener
// being closed before the function returned.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logInfo("starting HTTP server on %s", s.Addr())
		err := s.bindAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var serveErr error
	select {
	case serveErr = <-errCh:
		s.logError("HTTP server failed: %v", serveErr)
	case <-ctx.Done():
		s.logInfo("shutting down server")
	}

	_ = s.Shutdown()
	return serveErr
}

// bindAndServe binds the configured address and serves on the resulting
// listener. HTTP or HTTPS depending on whether TLS material was supplied.
//
// tls.NewListener is required: http.Server.Serve does NOT consult TLSConfig by
// itself, so setting TLSConfig alone would happily serve plain HTTP on a port
// the operator believes is HTTPS. ListenAndServeTLS wraps the listener the same
// way — doing it here explicitly is what lets the key pair be loaded at
// construction time instead of at serve time.
func (s *Server) bindAndServe() error {
	lis, err := net.Listen("tcp", s.Addr())
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.lis = lis
	s.mu.Unlock()
	if s.tlsCfg != nil {
		return s.httpSrv.Serve(tls.NewListener(lis, s.tlsCfg))
	}
	return s.httpSrv.Serve(lis)
}

// Run is Serve with shutdown-signal handling built in: it watches SIGINT and
// SIGTERM and treats the first one as a cancel, so Ctrl-C and container stop
// both drain gracefully.
//
// It is the pre-split serverapp.RunHTTP behaviour, extracted. Callers that
// want to drive shutdown themselves (e.g. "peerdrive all", which owns its own
// signal handling and a shared shutdown hook) call Serve instead.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(ctx) }()

	select {
	case err := <-errCh:
		return err
	case <-quit:
		// A signal is a cancel, not an error: fall through to Serve's graceful
		// drain, then return whatever Serve reports.
		cancel()
		return <-errCh
	}
}

// Shutdown drains outstanding requests for up to Config.ShutdownTimeout, then
// closes the listener hard. In-flight requests that do not finish in time are
// dropped, which is the point of bounding the drain.
//
// Returns nil unless the forced close itself fails.
func (s *Server) Shutdown() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := s.httpSrv.Shutdown(ctx); err != nil {
		s.logWarn("graceful shutdown timed out, forcing close: %v", err)
		if cerr := s.httpSrv.Close(); cerr != nil {
			s.logWarn("force close: %v", cerr)
			return cerr
		}
	}
	return nil
}

// Close tears the listener down immediately, dropping in-flight requests. Use
// it only where a drain is pointless; prefer Shutdown.
func (s *Server) Close() error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Close()
}
