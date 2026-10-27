package main

import (
	"log"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// ProcInfo 一个进程的名字、完整路径与启动时间。
type ProcInfo struct {
	Name    string
	Path    string // 完整 exe 路径；拿不到时为空（权限不足等）
	PID     uint32
	Created time.Time
}

// listProcesses 用 Toolhelp 快照枚举全部进程及其创建时间与完整路径。
//
// 为什么需要创建时间：判断「最后运行的程序」= 创建时间最晚的那个匹配进程（方案 A）。
// 为什么需要完整路径：不同游戏共用同一个引擎 exe（SiglusEngine.exe / RealLive.exe
// 这类视觉小说引擎）时，只靠进程名根本无法区分。路径是唯一可靠的标识。
//
// 拿不到创建时间的进程（权限不足/已退出）直接跳过，避免误判。
func listProcesses() ([]ProcInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafeSizeofProcessEntry32())

	var out []ProcInfo
	err = windows.Process32First(snap, &entry)
	for err == nil {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if name != "" {
			if created, path, e2 := processInfo(entry.ProcessID); e2 == nil {
				out = append(out, ProcInfo{
					Name:    name,
					Path:    path,
					PID:     entry.ProcessID,
					Created: created,
				})
			}
		}
		err = windows.Process32Next(snap, &entry)
	}
	return out, nil
}

// processInfo 一次 OpenProcess 同时读取进程创建时间与完整 exe 路径。
//
// 合并成一次调用是有意的：每秒都要枚举全部进程（几百个），
// 句柄开关是这条热路径上最贵的操作，能从两次减到一次就别浪费时间。
func processInfo(pid uint32) (time.Time, string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}, "", err
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, "", err
	}
	created := filetimeToTime(creation)

	// 路径拿不到不算错误：受保护进程（系统进程、反作弊保护的进程）会拒绝，
	// 这种进程不可能是用户添加的游戏，留给调用方按进程名兜底即可。
	path := queryProcessPath(h)
	return created, path, nil
}

// queryProcessPath 读取进程的完整映像路径。
//
// 缓冲区从 260（MAX_PATH）起步并在 ERROR_INSUFFICIENT_BUFFER 时按需翻倍，
// 因为启用了长路径的系统上 exe 路径可能远超 MAX_PATH。
func queryProcessPath(h windows.Handle) string {
	size := uint32(windows.MAX_PATH)
	buf := make([]uint16, size)
	for i := 0; i < 4; i++ { // 260 → 520 → 1040 → 2080，足够覆盖极端长路径
		n := size
		err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n)
		if err == nil {
			if n == 0 {
				return ""
			}
			return windows.UTF16ToString(buf[:n])
		}
		if err != windows.ERROR_INSUFFICIENT_BUFFER {
			return ""
		}
		size *= 2
		buf = make([]uint16, size)
	}
	return ""
}

// filetimeToTime FILETIME（1601-01-01 起的 100ns 计数）转 time.Time。
func filetimeToTime(ft windows.Filetime) time.Time {
	n := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	const epochDiff = 116444736000000000 // 1601 → 1970 的 100ns 间隔
	return time.Unix(0, (n-epochDiff)*100)
}

// normProcess 进程名归一化：小写、去 .exe、去空白。
func normProcess(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ".exe")
	return strings.TrimSpace(s)
}

// normPath 路径归一化，用于比对「是不是同一个 exe」。
//
// Windows 路径大小写不敏感，斜杠也可能混用（"D:\a\b.exe" vs "d:/a/b.exe"），
// 所以统一转小写 + 换回反斜杠 + 清理 ./.. 。拿不到路径时返回空串。
func normPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "/", `\`)
	p = filepath.Clean(p)
	return strings.ToLower(p)
}

// ============================================================
// 轻量日志（避免 winproc 直接依赖上层状态）
// ============================================================

var pathMissWarned = map[uint32]bool{}

// notePathMiss 记录「这个进程名匹配上了但拿不到路径」的歧义情况，
// 只记一次，免得每秒刷屏。返回是否应当记录。
func notePathMiss(pid uint32) bool {
	if pathMissWarned[pid] {
		return false
	}
	pathMissWarned[pid] = true
	return true
}

func logPathMiss(p *ProcInfo, candidates []*Game) {
	if !notePathMiss(p.PID) {
		return
	}
	names := make([]string, 0, len(candidates))
	for _, g := range candidates {
		names = append(names, g.Display)
	}
	log.Printf("[match] 进程 %s (PID %d) 读不到完整路径，按进程名匹配到 %d 个候选 %v，按启动时间取舍",
		p.Name, p.PID, len(candidates), names)
}
