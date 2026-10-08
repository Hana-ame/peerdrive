package signalframe

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWriter is a recording JSONWriter: it captures every value written and
// every deadline set, and can be told to fail writes (for failure-path tests).
type fakeWriter struct {
	mu        sync.Mutex
	got       []any
	deadlines []time.Time
	writeErr  error // when set, WriteJSON returns it
	closed    bool
}

func (f *fakeWriter) SetWriteDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadlines = append(f.deadlines, t)
	f.mu.Unlock()
	return nil
}

func (f *fakeWriter) WriteJSON(v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.got = append(f.got, v)
	return nil
}

func (f *fakeWriter) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeWriter) snapshot() (got []any, deadlines []time.Time, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]any(nil), f.got...), append([]time.Time(nil), f.deadlines...), f.closed
}

// TestNewMessage covers construction: payload marshaling, nil payload, and the
// historical "unmarshalable payload → empty Payload, no error" behavior.
func TestNewMessage(t *testing.T) {
	cases := []struct {
		name    string
		typ     MessageType
		dst     string
		payload any
		want    func(m Message) bool
	}{
		{
			name: "offer with struct payload", typ: MsgOffer, dst: "peer-2",
			payload: map[string]any{"connectionId": "c1", "type": "data"},
			want:    func(m Message) bool { return m.Type == MsgOffer && m.Dst == "peer-2" && string(m.Payload) == `{"connectionId":"c1","type":"data"}` },
		},
		{
			name: "heartbeat with nil payload", typ: MsgHeartbeat, dst: "",
			payload: nil,
			want:    func(m Message) bool { return m.Type == MsgHeartbeat && m.Dst == "" && m.Payload == nil },
		},
		{
			name: "unmarshalable payload leaves Payload empty", typ: MsgOffer, dst: "x",
			payload: make(chan int),
			want:    func(m Message) bool { return m.Type == MsgOffer && m.Payload == nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewMessage(tc.typ, tc.dst, tc.payload)
			if !tc.want(got) {
				t.Fatalf("NewMessage(%s, %q, %v) = %+v, want match", tc.typ, tc.dst, tc.payload, got)
			}
		})
	}
}

// TestMessageEncode checks the wire format: fields present/omitted per the
// json tags, payload passed through verbatim as RawMessage.
func TestMessageEncode(t *testing.T) {
	cases := []struct {
		name string
		m    Message
		want string
	}{
		{
			name: "full frame",
			m:    Message{Type: MsgOffer, Src: "a", Dst: "b", Payload: json.RawMessage(`{"x":1}`)},
			want: `{"type":"OFFER","src":"a","dst":"b","payload":{"x":1}}`,
		},
		{
			name: "omitempty fields elided",
			m:    Message{Type: MsgHeartbeat},
			want: `{"type":"HEARTBEAT"}`,
		},
		{
			name: "raw payload kept verbatim",
			m:    Message{Type: MsgCandidate, Dst: "b", Payload: json.RawMessage(`{"candidate":null}`)},
			want: `{"type":"CANDIDATE","dst":"b","payload":{"candidate":null}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.m.Encode()
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if string(b) != tc.want {
				t.Fatalf("Encode = %s, want %s", b, tc.want)
			}
		})
	}
}

// TestSenderSend_Success covers the happy path: the message reaches the
// underlying writer and a write deadline is applied.
func TestSenderSend_Success(t *testing.T) {
	fw := &fakeWriter{}
	s := NewSender(fw, 5*time.Second)
	if err := s.Send(NewMessage(MsgOpen, "", nil)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, deadlines, _ := fw.snapshot()
	if len(got) != 1 {
		t.Fatalf("writer saw %d writes, want 1", len(got))
	}
	if len(deadlines) != 1 {
		t.Fatalf("writer saw %d deadlines, want 1", len(deadlines))
	}
	want := time.Now().Add(5 * time.Second)
	if d := deadlines[0]; d.Before(want.Add(-time.Second)) || d.After(want.Add(time.Second)) {
		t.Fatalf("deadline = %v, want ≈ now+5s (%v)", d, want)
	}
}

// TestSenderSend_NotConnected: no writer attached → ErrNotConnected.
func TestSenderSend_NotConnected(t *testing.T) {
	s := NewSender(nil, time.Second)
	if err := s.Send(NewMessage(MsgOpen, "", nil)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Send without writer = %v, want ErrNotConnected", err)
	}
	// After SetWriter(nil) (detach) the same must hold.
	fw := &fakeWriter{}
	s.SetWriter(fw)
	s.SetWriter(nil)
	if err := s.Send(NewMessage(MsgOpen, "", nil)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Send after detach = %v, want ErrNotConnected", err)
	}
}

// TestSenderSend_WriteError: a failing underlying writer propagates its error.
func TestSenderSend_WriteError(t *testing.T) {
	fw := &fakeWriter{writeErr: errors.New("websocket: write timeout")}
	s := NewSender(fw, time.Second)
	err := s.Send(NewMessage(MsgOpen, "", nil))
	if err == nil || err.Error() != "websocket: write timeout" {
		t.Fatalf("Send = %v, want propagated write error", err)
	}
}

// TestSenderClose: Close detaches the writer and closes it (idempotent).
func TestSenderClose(t *testing.T) {
	fw := &fakeWriter{}
	s := NewSender(fw, time.Second)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, closed := fw.snapshot(); !closed {
		t.Fatal("underlying writer not closed")
	}
	if err := s.Send(NewMessage(MsgOpen, "", nil)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Send after Close = %v, want ErrNotConnected", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestSenderConcurrent hammers Send from many goroutines: every write must be
// serialized (run this under -race), none lost, none duplicated.
func TestSenderConcurrent(t *testing.T) {
	fw := &fakeWriter{}
	s := NewSender(fw, time.Second)
	const goroutines = 16
	const perG = 100
	var wg sync.WaitGroup
	var fails atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := s.Send(NewMessage(MsgHeartbeat, fmt.Sprintf("g%d", g), nil)); err != nil {
					fails.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	got, _, _ := fw.snapshot()
	if fails.Load() != 0 {
		t.Fatalf("%d concurrent Sends failed", fails.Load())
	}
	if len(got) != goroutines*perG {
		t.Fatalf("writer saw %d writes, want %d", len(got), goroutines*perG)
	}
	// Serialization proof: a map keyed by a token would race under -race if the
	// Sender did not hold its lock across WriteJSON; this is the -race tripwire.
}
