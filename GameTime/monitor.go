package main

import (
	"log"
	"sync"
	"time"
)

// ============================================================
// 监控主循环
//
// 每秒一次：
//   1. 枚举进程 → 找出创建时间最晚的已添加程序（方案 A）
//   2. 维护会话状态机（开始/切换/结束 → 落盘）
//   3. 发 UDP：有程序跑发 running:true + 会话时长；没程序跑发 running:false + 今日总时长
//
// 「最后运行的程序」判定用创建时间最晚，与固件显示的语义一致：
// 开了启动器又开游戏，OLED 显示游戏（更晚启动的那个）。
// ============================================================

var (
	monitorMu      sync.Mutex
	monitorStop    chan struct{}
	monitorDone    chan struct{}
	monitorRunning bool
)

// monitorState 主循环私有状态。
type monitorState struct {
	sender  *udpSender
	active  *activeSession
	lastCfg Config
}

// StartMonitor 启动（或重启）监控循环。
func StartMonitor() {
	monitorMu.Lock()
	if monitorRunning {
		monitorMu.Unlock()
		return
	}
	monitorStop = make(chan struct{})
	monitorDone = make(chan struct{})
	monitorRunning = true
	stop, done := monitorStop, monitorDone
	monitorMu.Unlock()

	go func() {
		defer close(done)
		runMonitor(stop)
		monitorMu.Lock()
		monitorRunning = false
		monitorMu.Unlock()
	}()
}

// StopMonitor 停止监控循环并把进行中的会话收尾落盘。
func StopMonitor() {
	monitorMu.Lock()
	stop, done, running := monitorStop, monitorDone, monitorRunning
	monitorMu.Unlock()
	if !running {
		return
	}
	close(stop)
	<-done
}

func runMonitor(stop <-chan struct{}) {
	st := &monitorState{sender: newUDPSender()}
	defer st.sender.Close()
	defer st.finishActive()

	log.Printf("[monitor] 已启动")
	for {
		cfg, err := LoadConfig()
		if err != nil {
			log.Printf("[monitor] 读配置失败: %v", err)
			cfg = defaultConfig()
		}
		st.lastCfg = cfg

		st.tick(cfg)

		interval := time.Duration(cfg.IntervalSec) * time.Second
		if interval < time.Second {
			interval = time.Second
		}
		select {
		case <-stop:
			log.Printf("[monitor] 已停止")
			return
		case <-time.After(interval):
		}
	}
}

// tick 执行一轮采样 + 发送。
func (st *monitorState) tick(cfg Config) {
	now := time.Now()

	procs, err := listProcesses()
	var game *Game
	var proc *ProcInfo
	if err == nil {
		game, proc = pickLatest(cfg.Games, procs)
	}

	// ---- 会话状态机 ----
	//
	// 会话身份 = 显示的游戏（GameID） + 具体那个进程实例（PID）。
	// 用 PID 而不是进程名判断「重启过」：同引擎的不同游戏进程名完全一样
	// （都叫 SiglusEngine.exe），按名字比会把「切换游戏」误判成「没变」，
	// 导致切换后仍在给旧游戏计时。
	if game != nil && proc != nil {
		switch {
		case st.active == nil || st.active.GameID != game.ID:
			// 换游戏：结束旧的，开启新的
			st.finishActive()
			st.startActive(game, proc)
		case st.active.PID != proc.PID:
			// 同一个游戏重启了（新进程）：按新进程重开
			st.finishActive()
			st.startActive(game, proc)
		}
	} else {
		st.finishActive()
	}

	// ---- 组装 UDP 载荷 ----
	var payload udpPayload
	if st.active != nil && game != nil {
		payload = udpPayload{
			Running: true,
			Name:    game.Display,
			Up:      sessionUp(st.active, now),
			Today:   TodayTotalSeconds() + sessionUp(st.active, now),
		}
	} else {
		// 无程序运行：发今日总时长，固件会显示 TODAY TOTAL
		payload = udpPayload{
			Running: false,
			Name:    "",
			Up:      0,
			Today:   TodayTotalSeconds(),
		}
	}

	sendErr := st.sender.Send(cfg.Target(), payload)

	// ---- 更新前端状态 ----
	setLive(func(l *LiveStatus) {
		l.Running = payload.Running
		l.Today = payload.Today
		l.Target = cfg.Target()
		l.Autostart = cfg.Autostart
		if payload.Running {
			l.Name = payload.Name
			l.Up = payload.Up
			l.ExeName = ""
			if proc != nil {
				l.ExeName = proc.Name
			}
		} else {
			l.Name = ""
			l.Up = 0
			l.ExeName = ""
		}
		if sendErr != nil {
			l.ESP32OK = false
			l.ESP32Err = sendErr.Error()
		} else {
			l.ESP32OK = true
			l.ESP32Err = ""
			l.LastSent = now.Unix()
		}
	})
}

// startActive 开启新会话。
func (st *monitorState) startActive(g *Game, p *ProcInfo) {
	st.active = &activeSession{
		GameID:  g.ID,
		Process: p.Name,
		PID:     p.PID,
		Start:   p.Created,
	}
	writeActive(&activeSnapshot{
		GameID:  g.ID,
		Process: p.Name,
		Display: g.Display,
		Start:   p.Created.Unix(),
	})
	log.Printf("[session] 开始 %s (%s pid=%d) 起于 %s", g.Display, p.Name, p.PID, p.Created.Format("15:04:05"))
}

// finishActive 结束当前会话并落盘。
func (st *monitorState) finishActive() {
	if st.active == nil {
		return
	}
	end := time.Now()
	dur := sessionUp(st.active, end)
	rec := SessionRecord{
		GameID:  st.active.GameID,
		Process: st.active.Process,
		Display: displayOf(st.active.GameID),
		Start:   st.active.Start.Unix(),
		End:     end.Unix(),
		Dur:     dur,
		Day:     todayKey(st.active.Start),
	}
	st.active = nil
	writeActive(nil)

	if dur <= 0 {
		return
	}
	appendSession(rec)
	log.Printf("[session] 结束 %s 时长 %s", rec.Display, fmtDuration(dur))
}

// displayOf 按 Game.ID 取当前显示名（改名后仍能对上）。
func displayOf(gameID string) string {
	cfg, err := LoadConfig()
	if err != nil {
		return ""
	}
	for _, g := range cfg.Games {
		if g.ID == gameID {
			return g.Display
		}
	}
	return ""
}

// fmtDuration 把秒格式化成 1h02m03s。
func fmtDuration(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return itoa(h) + "h" + pad2(m) + "m" + pad2(s) + "s"
	}
	if m > 0 {
		return itoa(m) + "m" + pad2(s) + "s"
	}
	return itoa(s) + "s"
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func pad2(v int64) string {
	if v < 10 {
		return "0" + itoa(v)
	}
	return itoa(v)
}
