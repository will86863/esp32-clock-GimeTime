package main

import (
	"os"
	"testing"
)

// TestListProcessesRealPaths 真实枚举本机进程，验证路径读取确实生效。
//
// 只在 Windows 上有意义；读不到路径的进程（受保护的系统进程）允许为空，
// 但至少要有一批进程能拿到完整路径，否则说明 QueryFullProcessImageName 用错了。
func TestListProcessesRealPaths(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatalf("枚举进程失败: %v", err)
	}
	if len(procs) < 10 {
		t.Fatalf("进程数异常少: %d", len(procs))
	}

	var withPath, withoutPath int
	for _, p := range procs {
		if p.Path != "" {
			withPath++
			if _, err := os.Stat(p.Path); err != nil {
				t.Errorf("进程 %s (pid=%d) 的路径不可访问: %s", p.Name, p.PID, p.Path)
			}
			// 路径必须以进程名结尾，否则说明读串了
			if !hasSuffixFold(p.Path, p.Name) {
				t.Errorf("进程 %s 的路径 %q 与进程名不匹配", p.Name, p.Path)
			}
		} else {
			withoutPath++
		}
	}

	t.Logf("共 %d 个进程：%d 个读到路径，%d 个读不到（受保护进程）",
		len(procs), withPath, withoutPath)

	if withPath < len(procs)/2 {
		t.Fatalf("只有 %d/%d 个进程读到路径，路径读取可能没生效", withPath, len(procs))
	}
}

// TestPickLatestOnRealProcesses 用真实进程表跑一遍匹配，确认不会 panic、
// 且结果自洽（命中的 Game 与进程路径确实对应）。
func TestPickLatestOnRealProcesses(t *testing.T) {
	procs, err := listProcesses()
	if err != nil {
		t.Fatalf("枚举进程失败: %v", err)
	}

	// 拿本机真实存在的一个进程造一个 Game，应当能被匹配到
	var target *ProcInfo
	for i := range procs {
		if procs[i].Path != "" {
			target = &procs[i]
			break
		}
	}
	if target == nil {
		t.Skip("没有读到任何进程路径，跳过")
	}

	games := []Game{{
		ID:      "self",
		Exe:     target.Path,
		Process: target.Name,
		Display: "SELF",
	}}

	g, p := pickLatest(games, procs)
	if g == nil || p == nil {
		t.Fatalf("应当匹配到 %s，实际 nil", target.Path)
	}
	if g.ID != "self" {
		t.Fatalf("匹配到错误条目: %+v", g)
	}
	if normPath(p.Path) != normPath(target.Path) {
		t.Fatalf("匹配到错误进程: %s，期望 %s", p.Path, target.Path)
	}
	t.Logf("匹配到 %s (pid=%d)", p.Path, p.PID)
}

// hasSuffixFold 大小写不敏感的后缀判断。
func hasSuffixFold(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	a := s[len(s)-len(suffix):]
	return equalFold(a, suffix)
}
