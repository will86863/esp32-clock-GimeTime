package main

import (
	"time"
)

// ============================================================
// 会话状态机
//
// 规则（用户确认）：
//   - 当前程序 = 已添加程序里「创建时间最晚」的匹配进程（方案 A）
//   - up       = 本次会话已运行秒数，从「该进程创建时间」起算
//   - 不记录空闲时间：没有已添加程序在跑时不写会话
//   - today    = 已添加程序今日会话秒数之和（无程序运行时发给 ESP32）
//
// 为什么 up 用进程创建时间而不是本程序开始观察的时间：
// 上位机被强杀后重启，时长能无缝接续，不会从 0 重新计。
//
// ★ 进程分辨机制（v1.2.0）
//
// 只按进程名匹配是不可行的：大量视觉小说用同一套引擎，
// Rewrite Harvest / CLANNAD SS 都是 SiglusEngine*.exe，Never7 / Tomoyo After
// 都是同一族 RealLive*.exe。一旦用户添加了两个共用引擎的不同游戏，
// 旧逻辑会「同一个进程名只能加一个」（AddGame 直接拒绝），即便绕过，
// pickLatest 也只会认第一个，另一个永远统计不到。
//
// 所以匹配身份改成 **exe 完整路径**：
//   1. 优先用 normPath(进程完整路径) == normPath(Game.Exe) 精确命中；
//   2. 读不到路径（受保护进程）时才退回按进程名匹配；
//   3. 进程名有多个候选且拿不到路径时，记一条日志说明是按名字猜的。
// ============================================================

// activeSession 正在进行的会话。
type activeSession struct {
	GameID  string    // 对应 Game.ID
	Process string    // 进程名（原始大小写）
	PID     uint32    // 进程 PID，用于识别「同一个游戏重启了」
	Start   time.Time // 会话起点 = 进程创建时间
}

// matcher 已添加程序的匹配索引，每轮按当前配置重建。
type matcher struct {
	byPath map[string]*Game // normPath(Game.Exe) → Game
	byName map[string][]*Game
}

// buildMatcher 建立匹配索引。
func buildMatcher(games []Game) *matcher {
	m := &matcher{
		byPath: make(map[string]*Game, len(games)),
		byName: make(map[string][]*Game, len(games)),
	}
	for i := range games {
		g := &games[i]
		if k := normPath(g.Exe); k != "" {
			m.byPath[k] = g
		}
		if k := normProcess(g.Process); k != "" {
			m.byName[k] = append(m.byName[k], g)
		}
	}
	return m
}

// match 判断一个进程属于哪个已添加程序。
//
// 路径命中优先——这是唯一能区分「同引擎不同游戏」的依据；
// 只有在进程路径读不到时才退回按名字匹配。
func (m *matcher) match(p *ProcInfo) *Game {
	if k := normPath(p.Path); k != "" {
		if g, ok := m.byPath[k]; ok {
			return g
		}
		// 路径读到了但不在添加列表里 —— 说明是另一个程序，直接不认。
		// 这里不能退回按名字匹配，否则又回到「同引擎游戏串味」的老问题。
		return nil
	}

	candidates := m.byName[normProcess(p.Name)]
	switch len(candidates) {
	case 0:
		return nil
	case 1:
		return candidates[0]
	default:
		// 名字有歧义、路径又读不到：只能按启动时间最早的候选兜底，
		// 并记日志提示用户（这种情况基本只出现在受保护的系统级进程上）。
		logPathMiss(p, candidates)
		return candidates[0]
	}
}

// pickLatest 从进程表里挑出「创建时间最晚」的已添加程序。
//
// games 为已添加程序，procs 为当前全部进程。
// 返回命中的 Game 指针与对应进程；没有命中返回 nil。
func pickLatest(games []Game, procs []ProcInfo) (*Game, *ProcInfo) {
	if len(games) == 0 {
		return nil, nil
	}
	m := buildMatcher(games)

	var bestGame *Game
	var bestProc *ProcInfo
	for i := range procs {
		p := &procs[i]
		g := m.match(p)
		if g == nil {
			continue
		}
		if bestProc == nil || p.Created.After(bestProc.Created) {
			bestGame, bestProc = g, p
		}
	}
	return bestGame, bestProc
}

// sessionUp 返回会话已运行秒数（向下取整，负数归零）。
func sessionUp(s *activeSession, now time.Time) int64 {
	if s == nil {
		return 0
	}
	sec := int64(now.Sub(s.Start).Seconds())
	if sec < 0 {
		return 0
	}
	return sec
}
