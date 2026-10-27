package main

import (
	"testing"
	"time"
)

// mkProc 构造一个测试用进程。
func mkProc(name, path string, pid uint32, created time.Time) ProcInfo {
	return ProcInfo{Name: name, Path: path, PID: pid, Created: created}
}

var base = time.Date(2026, 10, 27, 20, 0, 0, 0, time.Local)

// TestNormPath 路径归一化：大小写、斜杠方向、./.. 都应视为同一路径。
func TestNormPath(t *testing.T) {
	cases := [][2]string{
		{`D:\GAME\a\game.exe`, `d:/game/a/GAME.EXE`},
		{`D:\GAME\a\game.exe`, `D:\GAME\a\..\a\game.exe`},
		{`D:\GAME\a\game.exe`, `D:\GAME\a\.\game.exe`},
	}
	for _, c := range cases {
		if normPath(c[0]) != normPath(c[1]) {
			t.Fatalf("应当视为同一路径:\n %q -> %q\n %q -> %q",
				c[0], normPath(c[0]), c[1], normPath(c[1]))
		}
	}
	if normPath(`D:\a\game.exe`) == normPath(`D:\b\game.exe`) {
		t.Fatal("不同目录下的同名 exe 不能归一化成同一路径")
	}
}

// TestPickLatestDistinguishesSameEngine 同引擎不同游戏必须能区分开。
// 这是本次修复的核心场景：Rewrite Harvest 与 CLANNAD SS 都叫 SiglusEngine*.exe。
func TestPickLatestDistinguishesSameEngine(t *testing.T) {
	games := []Game{
		{ID: "rewrite", Process: "SiglusEngine_SteamEN.exe", Exe: `D:\Games\Rewrite\SiglusEngine_SteamEN.exe`, Display: "Rewrite"},
		{ID: "clannad", Process: "SiglusEngine_SteamEN.exe", Exe: `D:\Games\CLANNAD\SiglusEngine_SteamEN.exe`, Display: "CLANNAD"},
	}

	// 只有 CLANNAD 在跑
	procs := []ProcInfo{
		mkProc("SiglusEngine_SteamEN.exe", `D:\Games\CLANNAD\SiglusEngine_SteamEN.exe`, 200, base),
	}
	g, p := pickLatest(games, procs)
	if g == nil || g.ID != "clannad" {
		t.Fatalf("应当命中 clannad，实际 %+v", g)
	}
	if p.PID != 200 {
		t.Fatalf("进程应为 PID 200，实际 %d", p.PID)
	}

	// 两个都在跑：取启动时间更晚的那个（CLANNAD 更晚启动）
	procs = append(procs, mkProc("SiglusEngine_SteamEN.exe", `D:\Games\Rewrite\SiglusEngine_SteamEN.exe`, 100, base.Add(-time.Hour)))
	g, p = pickLatest(games, procs)
	if g == nil || g.ID != "clannad" {
		t.Fatalf("后启动的 clannad 应胜出，实际 %+v", g)
	}
	if p.PID != 200 {
		t.Fatalf("进程应为 PID 200，实际 %d", p.PID)
	}
}

// TestPickLatestPathBeatsName 路径命中时，不能因为名字相同就误配到别的游戏。
func TestPickLatestPathBeatsName(t *testing.T) {
	games := []Game{
		{ID: "a", Process: "game.exe", Exe: `D:\A\game.exe`, Display: "A"},
	}
	procs := []ProcInfo{
		// 同名 exe，但在别的目录 —— 不属于已添加的程序
		mkProc("game.exe", `D:\B\game.exe`, 300, base),
	}
	g, _ := pickLatest(games, procs)
	if g != nil {
		t.Fatalf("不同目录的同名 exe 不该命中，实际命中 %+v", g)
	}
}

// TestPickLatestFallbackByName 读不到路径时，唯一同名进程仍应命中。
func TestPickLatestFallbackByName(t *testing.T) {
	games := []Game{
		{ID: "x", Process: "EVER17PC.exe", Exe: `D:\GAME\Ever17\EVER17PC.exe`, Display: "EVER17"},
	}
	procs := []ProcInfo{
		// Path 为空 = 受保护进程，读不到路径
		mkProc("EVER17PC.exe", "", 400, base),
	}
	g, _ := pickLatest(games, procs)
	if g == nil || g.ID != "x" {
		t.Fatalf("拿不到路径时应按名字兜底命中 x，实际 %+v", g)
	}
}

// TestPickLatestNoMatch 没有匹配进程时返回 nil。
func TestPickLatestNoMatch(t *testing.T) {
	games := []Game{{ID: "x", Process: "game.exe", Exe: `D:\game.exe`}}
	procs := []ProcInfo{mkProc("other.exe", `D:\other.exe`, 500, base)}
	g, p := pickLatest(games, procs)
	if g != nil || p != nil {
		t.Fatalf("不应命中，实际 game=%+v proc=%+v", g, p)
	}
}

// TestPickLatestEmptyGames 空列表安全返回。
func TestPickLatestEmptyGames(t *testing.T) {
	if g, p := pickLatest(nil, nil); g != nil || p != nil {
		t.Fatal("空列表应返回 nil")
	}
}

// TestBuildMatcherPathKey 路径索引应与 exe 字段一致。
func TestBuildMatcherPathKey(t *testing.T) {
	games := []Game{{ID: "a", Process: "g.exe", Exe: `D:\X\g.exe`}}
	m := buildMatcher(games)
	if m.byPath[normPath(`d:/x/G.EXE`)] == nil {
		t.Fatal("路径索引缺失或归一化不一致")
	}
}
