package webrtc

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestConn 造一个不接 PeerConnection 的 Conn。
//
// attach 之后才有的 dc 这里留空，专门用来测「通道还没就绪」这条路径——
// 这条路径曾经是真实的故障点（Send 在 dc 为 nil 时会 panic 而不是报错）。
func newTestConn() *Conn {
	return &Conn{
		remote: "peer-test",
		id:     "conn-test",
		role:   RoleServer,
		log:    log.New(io.Discard, "", 0),
		done:   make(chan struct{}),
		onMsg:  make(chan Frame, 4),
	}
}

func TestConnNextStreamMonotonic(t *testing.T) {
	c := newTestConn()
	var last uint32
	for i := 1; i <= 100; i++ {
		got := c.nextStream()
		if got != uint32(i) {
			t.Fatalf("第 %d 次 nextStream = %d，want %d", i, got, i)
		}
		if got <= last {
			t.Fatalf("stream id 必须递增，%d 之后拿到 %d", last, got)
		}
		last = got
	}
}

func TestConnNextStreamConcurrentUnique(t *testing.T) {
	// stream id 在 OnMessage 与用户 goroutine 里都会被取，必须原子。
	// go test -race 会同时验证这里没有数据竞争。
	c := newTestConn()
	const n = 200
	var wg sync.WaitGroup
	seen := make([]uint32, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			seen[i] = c.nextStream()
		}(i)
	}
	wg.Wait()

	uniq := make(map[uint32]bool, n)
	for _, v := range seen {
		if v == 0 {
			t.Fatal("stream id 不应为 0（0 在 SendJSON 里表示「自动分配」）")
		}
		if uniq[v] {
			t.Fatalf("stream id 重复: %d", v)
		}
		uniq[v] = true
	}
	if len(uniq) != n {
		t.Fatalf("期望 %d 个唯一 id，实得 %d", n, len(uniq))
	}
}

func TestConnSendWithoutDataChannel(t *testing.T) {
	c := newTestConn()
	err := c.Send(Frame{Kind: KindData, Stream: 1, Data: []byte("x")})
	if err == nil {
		t.Fatal("dc 为 nil 时 Send 应返回错误")
	}
	if !strings.Contains(err.Error(), "DataChannel 尚未就绪") {
		t.Errorf("错误文案变了: %v", err)
	}
}

func TestConnSendAfterClose(t *testing.T) {
	c := newTestConn()
	c.fail(ErrClosed)
	if err := c.Send(Frame{Kind: KindData, Stream: 1}); !errors.Is(err, ErrClosed) {
		t.Errorf("关闭后 Send 应返回 ErrClosed，实得 %v", err)
	}
}

func TestConnSendJSONMarshalError(t *testing.T) {
	// 不可序列化的值必须在碰 dc 之前就失败，否则错误会被误报成「通道未就绪」。
	c := newTestConn()
	err := c.SendJSON(KindMetadata, 1, make(chan int))
	if err == nil {
		t.Fatal("channel 无法 JSON 序列化，应返回错误")
	}
	if strings.Contains(err.Error(), "DataChannel") {
		t.Errorf("不应报成通道问题: %v", err)
	}
}

func TestConnNextDeliversFrame(t *testing.T) {
	c := newTestConn()
	want := Frame{Kind: KindAck, Stream: 3, Data: []byte(`{"ok":true}`)}
	c.onMsg <- want

	got, err := c.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.Kind != want.Kind || got.Stream != want.Stream || string(got.Data) != string(want.Data) {
		t.Errorf("Next = %+v，want %+v", got, want)
	}
}

func TestConnNextOnClosed(t *testing.T) {
	c := newTestConn()
	c.fail(ErrClosed)

	// 已经关闭的连接要立刻返回，不能等 channel 上的消息。
	done := make(chan error, 1)
	go func() {
		_, err := c.Next(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("Next = %v，want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("已关闭的连接上 Next 应立刻返回，而不是阻塞")
	}
}

func TestConnNextContextCancel(t *testing.T) {
	c := newTestConn()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Next(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Next = %v，want context.Canceled", err)
	}
}

func TestConnFailIdempotent(t *testing.T) {
	// fail 用 sync.Once 包着，重复调用不能重复 close(done)（会 panic）。
	c := newTestConn()
	for i := 0; i < 5; i++ {
		c.fail(ErrClosed)
	}
	if err := c.Err(); !errors.Is(err, ErrClosed) {
		t.Errorf("Err = %v", err)
	}
	if !c.closed.Load() {
		t.Error("closed 应被置位")
	}
}

func TestConnCloseWithoutPeerConnection(t *testing.T) {
	// pc 为 nil 时 Close 只置状态，不应 panic。
	c := newTestConn()
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	select {
	case <-c.Done():
	default:
		t.Error("Close 之后 Done 应当已关闭")
	}
}

func TestConnAccessors(t *testing.T) {
	c := newTestConn()
	if c.Remote() != "peer-test" {
		t.Errorf("Remote = %q", c.Remote())
	}
	if c.ID() != "conn-test" {
		t.Errorf("ID = %q", c.ID())
	}
	if c.Messages() == nil {
		t.Error("Messages 不应为 nil")
	}
}

func TestConnMessagesNeverClosed(t *testing.T) {
	// fail 特意不关 onMsg：OnMessage 回调可能正卡在 send 上，关了会 panic。
	// 语义统一由 done 表达，这里把这个约定钉住。
	c := newTestConn()
	c.fail(ErrClosed)
	select {
	case v, ok := <-c.onMsg:
		if !ok {
			t.Error("onMsg 不应被关闭")
		}
		_ = v
	default:
	}
}

func TestHexHead(t *testing.T) {
	// n 大于长度时取全长；空输入与空切片返回占位；NUL 与高位字节都要保留。
	cases := []struct {
		name string
		in   []byte
		n    int
		want string
	}{
		{"整帧头", []byte{0x01, 0x00, 0x00, 0x00, 0x01, 0x7b}, 6, "01000000017b"},
		{"n 超过长度", []byte{0x01, 0x00}, 6, "0100"},
		{"nil", nil, 6, "(空)"},
		{"空切片", []byte{}, 3, "(空)"},
		{"高位字节", []byte{0xff, 0x00}, 2, "ff00"},
		{"n 为 0", []byte{0x01, 0x02, 0x03}, 0, "(空)"},
	}
	for _, c := range cases {
		if got := hexHead(c.in, c.n); got != c.want {
			t.Errorf("%s: hexHead(%v, %d) = %q，want %q", c.name, c.in, c.n, got, c.want)
		}
	}
}

func TestServerConnsCount(t *testing.T) {
	// Conns() 是对 live 的只读视图，空的 Server 应返回 0 而不是 panic。
	s := &Server{live: map[*Conn]struct{}{}}
	if n := s.Conns(); n != 0 {
		t.Errorf("Conns = %d，want 0", n)
	}

	c := newTestConn()
	s.live[c] = struct{}{}
	if n := s.Conns(); n != 1 {
		t.Errorf("Conns = %d，want 1", n)
	}
}
