package btloader

import "encoding/json"

// transport moves one op across the process boundary.
//
// Today there is exactly one implementation (the Windows DLL). The interface
// exists so the runtime can be swapped for an in-process or sidecar transport
// without touching the Client API, and so non-Windows builds can compile with a
// stub that always returns ErrUnavailable.
type transport interface {
	// Call executes one op. args is JSON-marshalled first; nil means no args.
	Call(op string, args any) (json.RawMessage, error)

	// Close stops the runtime behind the transport.
	Close() error
}

// envelope is the reply shape produced by the DLL (cmd/btbridge).
type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}
