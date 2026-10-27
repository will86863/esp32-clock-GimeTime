package main

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// startSink 起一个本地 UDP 接收器，返回地址与收包通道。
func startSink(t *testing.T) (*net.UDPAddr, <-chan udpPayload) {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("解析地址失败: %v", err)
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ch := make(chan udpPayload, 8)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var p udpPayload
			if json.Unmarshal(buf[:n], &p) == nil {
				select {
				case ch <- p:
				default:
				}
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr), ch
}

// recv 等一帧，超时即失败。
func recv(t *testing.T, ch <-chan udpPayload, what string) udpPayload {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: 2 秒内没收到 UDP 包", what)
		return udpPayload{}
	}
}

// TestUDPSenderBasic 基本发送：载荷与字段名必须和固件一致。
func TestUDPSenderBasic(t *testing.T) {
	addr, ch := startSink(t)
	s := newUDPSender()
	defer s.Close()

	want := udpPayload{Running: true, Name: "Rewrite", Up: 18121, Today: 18121}
	if err := s.Send(addr.String(), want); err != nil {
		t.Fatalf("第一次发送失败: %v", err)
	}
	if got := recv(t, ch, "首帧"); got != want {
		t.Fatalf("载荷不一致:\n got %+v\nwant %+v", got, want)
	}

	// 连续发多帧，确认复用同一个 socket 时也不出错
	for i := 0; i < 5; i++ {
		if err := s.Send(addr.String(), want); err != nil {
			t.Fatalf("第 %d 帧发送失败: %v", i+2, err)
		}
	}
	recv(t, ch, "复用 socket")
}

// TestUDPSenderTargetChange 目标地址变化时必须重建 socket 并送到新地址。
func TestUDPSenderTargetChange(t *testing.T) {
	s := newUDPSender()
	defer s.Close()

	addr1, ch1 := startSink(t)
	addr2, ch2 := startSink(t)

	if err := s.Send(addr1.String(), udpPayload{Name: "one"}); err != nil {
		t.Fatalf("发往 sink1 失败: %v", err)
	}
	if got := recv(t, ch1, "sink1"); got.Name != "one" {
		t.Fatalf("sink1 收到 %q，期望 one", got.Name)
	}

	if err := s.Send(addr2.String(), udpPayload{Name: "two"}); err != nil {
		t.Fatalf("发往 sink2 失败: %v", err)
	}
	if got := recv(t, ch2, "sink2"); got.Name != "two" {
		t.Fatalf("sink2 收到 %q，期望 two", got.Name)
	}
}

// TestUDPSenderRecoversAfterClose 模拟「socket 失效」：从外部把底层连接关掉，
// 下一次 Send 必须自动重建并送达，而不是永久报错。
// 这正是 Windows 上 10022 那个 bug 的回归测试。
func TestUDPSenderRecoversAfterClose(t *testing.T) {
	addr, ch := startSink(t)
	s := newUDPSender()
	defer s.Close()

	if err := s.Send(addr.String(), udpPayload{Name: "before"}); err != nil {
		t.Fatalf("初始发送失败: %v", err)
	}
	recv(t, ch, "初始帧")

	// 模拟网络抖动后 socket 被系统判死：直接关掉底层连接
	s.mu.Lock()
	_ = s.conn.Close()
	s.mu.Unlock()

	// 第一次写会失败（bad file descriptor），但不应崩溃
	_ = s.Send(addr.String(), udpPayload{Name: "broken"})

	// 再发一次必须自愈
	if err := s.Send(addr.String(), udpPayload{Name: "after"}); err != nil {
		t.Fatalf("socket 失效后未能自愈: %v", err)
	}
	got := recv(t, ch, "自愈帧")
	if got.Name != "after" {
		t.Fatalf("自愈后收到 %q，期望 after", got.Name)
	}
}

// TestUDPSenderEmptyTarget 未配置地址时要有明确错误。
func TestUDPSenderEmptyTarget(t *testing.T) {
	s := newUDPSender()
	defer s.Close()
	if err := s.Send("", udpPayload{}); err == nil {
		t.Fatal("空地址应当返回错误")
	}
}
