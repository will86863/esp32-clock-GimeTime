// Package main —— GameTime 上位机
//
// 与 ESP32 固件（SmallDesktopDisplay V1.6.8）的 UDP 协议约定：
//
//	端口 5005，载荷 JSON：{"running":bool,"name":string,"up":int,"today":int}
//
// 固件侧硬约束（读 SmallDesktopDisplay.ino 确认，固件不可改）：
//   - char gameName[24]                → name 必须 ≤23 字节
//   - oled_drawString 居中 len>16 不截断 → 可见宽度 ≤16 字符
//   - 因此显示名一律要求纯 ASCII 且 ≤16 字符（见 displayname.go）
//   - 5 秒收不到数据即视为未运行      → 必须每秒发送
//   - today 仅在 !running 时显示      → 无程序运行时发送今日总时长
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// ============================================================
// 协议
// ============================================================

// udpPayload 发往 ESP32 的 JSON 结构。
// 字段名必须与固件 deserializeJson 的取值键完全一致，不可改。
type udpPayload struct {
	Running bool   `json:"running"`
	Name    string `json:"name"`
	Up      int64  `json:"up"`
	Today   int64  `json:"today"`
}

// udpSender 持有 UDP 目标与连接，避免每秒重建 socket。
//
// 为什么不用 net.Dial（connected UDP）——这是 Windows 上的一个真实陷阱：
// Dial 会把 socket 绑定到「当时那条路由 + 那个网卡地址」上，一旦网络接口
// 状态变化（Wi-Fi 重连/漫游、休眠唤醒、切换网卡），这个已连接的 UDP socket
// 会永久失效，之后每次 Write 都返回 WSAEINVAL(10022)
// "wsasend: An invalid argument was supplied."，且不会自愈。
//
// 改成 ListenUDP + WriteToUDP：socket 绑在 0.0.0.0:0 上不锁定具体接口，
// 每个包都由内核重新查路由选源地址，网络抖动后自动恢复，不会卡死。
// 另外再加一层兜底：任何一次写失败就丢弃 socket，下一轮自动重建。
type udpSender struct {
	mu    sync.Mutex
	conn  *net.UDPConn
	raddr *net.UDPAddr
	addr  string
}

func newUDPSender() *udpSender { return &udpSender{} }

// Send 发送一帧数据。目标地址变化或上次写失败时自动重建 socket。
// 返回值 err 用于前端展示 ESP32 连通状态。
func (s *udpSender) Send(target string, p udpPayload) error {
	if target == "" {
		return fmt.Errorf("未配置 ESP32 地址")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil || s.addr != target || s.raddr == nil {
		s.reset()
		raddr, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			return err
		}
		// nil 表示绑定到 0.0.0.0:0 —— 不锁定任何网卡，每包重新选路
		conn, err := net.ListenUDP("udp4", nil)
		if err != nil {
			return err
		}
		s.conn, s.raddr, s.addr = conn, raddr, target
	}

	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = s.conn.WriteToUDP(data, s.raddr); err != nil {
		// 写失败：立刻作废这个 socket，下一轮 Send 会重建。
		// 网络恢复后最多 1 秒（一个发送间隔）就能自愈，不需要重启程序。
		s.reset()
		return err
	}
	return nil
}

// reset 关闭并清空底层连接（调用方需自行持锁）。
func (s *udpSender) reset() {
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.conn, s.raddr, s.addr = nil, nil, ""
}

// Close 关闭底层连接。
func (s *udpSender) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
}

// ============================================================
// 运行时状态（供前端轮询）
// ============================================================

// LiveStatus 概览页展示的实时状态。
type LiveStatus struct {
	Running   bool   `json:"running"`    // 当前是否有已添加程序在运行
	Name      string `json:"name"`       // 当前程序显示名
	Up        int64  `json:"up"`         // 当前会话秒数
	Today     int64  `json:"today"`      // 今日已添加程序合计秒数
	ESP32OK   bool   `json:"esp32ok"`    // 最近一次发送是否成功
	ESP32Err  string `json:"esp32err"`   // 最近一次错误
	Target    string `json:"target"`     // 当前目标 ip:port
	LastSent  int64  `json:"lastsent"`   // 最近成功发送的 unix 秒
	ExeName   string `json:"exename"`    // 当前进程名
	Version   string `json:"version"`    // 程序版本
	Autostart bool   `json:"autostart"`  // 开机自启是否开启
	StartedAt int64  `json:"startedat"`  // 本程序启动 unix 秒
}

var (
	liveMu sync.RWMutex
	live   = LiveStatus{}
)

func setLive(fn func(*LiveStatus)) {
	liveMu.Lock()
	fn(&live)
	liveMu.Unlock()
}

func getLive() LiveStatus {
	liveMu.RLock()
	defer liveMu.RUnlock()
	return live
}

// todayKey 返回本地时区的日期键，用于统计分桶。
func todayKey(t time.Time) string { return t.Format("2006-01-02") }
