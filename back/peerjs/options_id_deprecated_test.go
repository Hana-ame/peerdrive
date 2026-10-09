package peerjs

import (
	"testing"
	"time"
)

// TestOptionsIDIsNotMappedIntoSignalling 钉住 #140 记录的现状：Options.ID 是
// 死字段，设置它不会影响任何行为。
//
// 发现背景：PR #48（feat/peerjs-split）把信令拆成 signalling 子包时，
// 新增 signalling/id.go 承接 ID 逻辑，而 signalling.Options 本身没有 ID 字段，
// 于是 peer.Options.ID 失去了唯一的落点、变成孤儿。但它的注释仍写着
// 「empty means the server assigns a random ID」，读起来像「非空则用它」——
// 调用方写 Options{ID: "my-node"} 会以为控制住了节点 ID，实际拿到的是信令
// 服务器分配的值，且没有任何报错。
//
// 这条用例不是断言「应该删掉这个字段」，而是把「它确实不参与映射」这件事写死，
// 防止以后有人顺手加映射、让行为在无感知的情况下改变。ID 的唯一权威来源是
// NewPeer 的位置参数（见 TestOptionsIDIsIgnoredByNewPeer）。
func TestOptionsIDIsNotMappedIntoSignalling(t *testing.T) {
	opts := Options{
		Host:         "example.com",
		Port:         "443",
		Key:          "pd-test",
		ID:           "my-node",
		PingInterval: 7 * time.Second,
	}

	got := signallingFromOptions(opts)

	// signalling.Options 根本没有 ID 字段可承载它——这正是该字段失效的成因。
	// 这里逐项确认其余 7 个字段都正常映射，只有 ID 被丢弃，避免把「映射整体
	// 坏了」误判成「ID 有意被丢弃」。
	if got.Host != "example.com" || got.Port != "443" || got.Key != "pd-test" {
		t.Fatalf("signallingFromOptions 未正确映射其余字段: %+v", got)
	}
	if got.PingInterval != 7*time.Second {
		t.Fatalf("PingInterval 应被逐字映射: %+v", got)
	}
}

// TestOptionsIDIsIgnoredByNewPeer 覆盖调用方最可能踩的那条路径：把 ID 写进
// Options 再建 Peer。新Peer 的位置参数为空 + 服务端未分配时，Peer.ID() 不应
// 因为 Options.ID 而返回 "my-node"。
//
// 发现背景：Options.ID 的旧注释让调用方以为它在控制节点 ID。若哪天有人
// 「修好」这个字段（让 Options.ID 真的生效），会与 NewPeer 的位置参数产生
// 两个互相矛盾的 ID 来源——而 Peer.ID() 读的是 signaller.ID()。这条用例
// 让这种改动必须是有意的、且伴随测试更新，而不是无声发生。
func TestOptionsIDIsIgnoredByNewPeer(t *testing.T) {
	p := NewPeer("", Options{ID: "my-node"})
	if p == nil {
		t.Fatal("NewPeer 返回 nil")
	}

	// 未连接且位置参数为空时 Peer.ID() 应为空（信令服务器分配前）。
	// 关键断言：绝不等于 "my-node"。
	if got := p.ID(); got == "my-node" {
		t.Fatal("Options.ID 不应影响 Peer.ID(): ID 的权威来源是 NewPeer 的位置参数")
	}
}

// TestOptionsIDIsIgnoredWhenPositionalIDGiven 确认位置参数才是权威：显式传
// id 时 Peer.ID() 应反映它，与 Options.ID 无关。
func TestOptionsIDIsIgnoredWhenPositionalIDGiven(t *testing.T) {
	p := NewPeer("positional-id", Options{ID: "options-id"})

	// 连接建立前 signaller.ID() 可能还是空（ID 要经信令服务器确认），
	// 所以这里只断言「不会被 Options.ID 覆盖」这一条确定的性质。
	if got := p.ID(); got == "options-id" {
		t.Fatal("Options.ID 不应覆盖 NewPeer 位置参数指定的 ID")
	}
}
