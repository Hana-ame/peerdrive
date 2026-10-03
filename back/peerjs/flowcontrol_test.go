package peerjs

import (
	"testing"
	"time"
)

// TestSendFrame_FlowControl_Resumes Regression: SendFrame's built-in flow control
// must resume sending after a low-water event (not deadlock waiting).
//
// Discovery background: the old implementation had each serveFile register its own
// OnBufferedAmountLow (pion replacement-style callback); with concurrent requests,
// only the last registrant could receive the event, and the remaining goroutines
// dead-waited when bufferedAmount > threshold -> concurrent large-file pulls deadlocked.
// Fix: the callback is registered once when attach happens (lowWater broadcast), and
// waiting is unified inside SendFrame.
func TestSendFrame_FlowControl_Resumes(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	// Simulate slow peer consumption: bufferedAmount exceeds threshold
	dc.setBuffered(defaultBufferLowThreshold + 1024)

	done := make(chan error, 1)
	go func() {
		done <- c.SendFrame(map[string]any{"type": "data", "size": 4}, []byte{1, 2, 3, 4})
	}()

	// Sender should be blocked on flow control wait
	select {
	case err := <-done:
		t.Fatalf("should not return immediately at high water mark: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// Peer consumes: lower water mark + trigger low-water event
	dc.setBuffered(0)
	dc.emitLow()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("send failed after resumption: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not resume after low-water event (deadlocked)")
	}
}

// TestSendFrame_FlowControl_CloseAborts Regression: when the connection closes
// during flow control wait, it must exit immediately (otherwise the caller hangs --
// old implementation kept blocking until ctx cancellation).
//
// Discovery background: exposed while writing tests -- flow control wait must be
// interruptible: when the connection closes, the caller cannot hang (otherwise the
// upper-level requestFile blocks permanently).
func TestSendFrame_FlowControl_CloseAborts(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	dc.setBuffered(defaultBufferLowThreshold + 1024)

	done := make(chan error, 1)
	go func() {
		done <- c.SendFrame(map[string]any{"type": "data", "size": 4}, []byte{1, 2, 3, 4})
	}()

	// Confirm blocking, then close the connection
	time.Sleep(100 * time.Millisecond)
	c.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("should return an error after connection close, not success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not exit after connection close (hung)")
	}
}
