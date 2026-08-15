package peerjs

import (
	"testing"
	"time"
)

// TestSendFrame_FlowControl_Resumes 回归：SendFrame 内置流控在低水位事件
// 后必须恢复发送（不会死等）。
//
// 发现背景：旧实现每个 serveFile 各自注册 OnBufferedAmountLow（pion 替换式
// 回调），并发请求时只有最后一个注册者能收到事件，其余 goroutine 在
// bufferedAmount > 阈值时死等 → 并发大文件拉取卡死。修复：回调 attach 时
// 注册一次（lowWater 广播），等待统一走 SendFrame 内部。
func TestSendFrame_FlowControl_Resumes(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	// 模拟对端消费慢：bufferedAmount 超阈值
	dc.setBuffered(defaultBufferLowThreshold + 1024)

	done := make(chan error, 1)
	go func() {
		done <- c.SendFrame(map[string]any{"type": "data", "size": 4}, []byte{1, 2, 3, 4})
	}()

	// 发送方应阻塞在流控等待
	select {
	case err := <-done:
		t.Fatalf("高水位时不应立即返回: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// 对端消费：降水位 + 触发低水位事件
	dc.setBuffered(0)
	dc.emitLow()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("恢复后发送失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("低水位事件后未恢复（死等）")
	}
}

// TestSendFrame_FlowControl_CloseAborts 回归：流控等待中连接关闭必须立即
// 退出（否则调用方悬挂——旧实现 ctx 取消前一直卡住）。
//
// 发现背景：写测试时暴露——流控等待必须可中断：连接关闭时调用方不能悬挂（否则上层 requestFile 永久阻塞）
func TestSendFrame_FlowControl_CloseAborts(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	dc.setBuffered(defaultBufferLowThreshold + 1024)

	done := make(chan error, 1)
	go func() {
		done <- c.SendFrame(map[string]any{"type": "data", "size": 4}, []byte{1, 2, 3, 4})
	}()

	// 确认阻塞后关闭连接
	time.Sleep(100 * time.Millisecond)
	c.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("连接关闭后应返回错误而不是成功")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("连接关闭后未退出（悬挂）")
	}
}
