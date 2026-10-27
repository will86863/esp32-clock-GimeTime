package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"
	"sync"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const appVersion = "1.2.0"

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/appicon.png
var appIcon []byte

var (
	mainApp  *App
	quitOnce bool

	// quitting 为 true 时 OnBeforeClose 放行，真正退出进程
	quitting atomic.Bool

	// 拖放路径暂存：Go 侧收到 → 前端轮询取走
	dropMu          sync.Mutex
	dropPending     []string
	fileDropHandler func(x, y int, paths []string)

	// startHiddenFlag 由 -minimized 命令行设置，startup 里据此隐藏窗口
	startHiddenFlag bool

	// dropReady 前端是否已调用 runtime.OnFileDrop 挂上拖放监听
	dropReady atomic.Bool
)

func main() {
	initPaths()
	initLogging()
	initStats()

	// 解析 -minimized：开机自启时直接进托盘，不弹窗
	startHidden := hasFlag(os.Args[1:], "-minimized", "--minimized", "/minimized")
	startHiddenFlag = startHidden

	// 单实例：已有实例在跑就不再启动
	if !acquireSingleInstance() {
		log.Printf("[main] 已有实例在运行，退出")
		return
	}
	defer releaseSingleInstance()

	// 自启状态与注册表对齐（exe 路径变了会自动修正）
	if cfg, err := LoadConfig(); err == nil && cfg.Autostart {
		_ = SetAutostart(true)
	}

	StartMonitor()

	mainApp = NewApp(appVersion)

	// 拖放：Wails v2 的 OnFileDrop 能拿到真实绝对路径（浏览器拿不到）
	// 这里把路径暂存，前端轮询 TakeDropPath 取走。
	//
	// 注意：拖 .url 时 Wails 会传 ["", "", "C:\\...\\x.url"]（含空串与前导空格），
	// 所以必须过滤掉空项，否则前端按第一个取会拿到空串、弹窗不出现。
	fileDropHandler = func(x, y int, paths []string) {
		clean := make([]string, 0, len(paths))
		for _, p := range paths {
			if t := strings.TrimSpace(p); t != "" {
				clean = append(clean, t)
			}
		}
		if len(clean) == 0 {
			log.Printf("[drop] 收到 %d 个路径但全为空: %v", len(paths), paths)
			return
		}
		dropMu.Lock()
		dropPending = append(dropPending, clean...)
		dropMu.Unlock()
		log.Printf("[drop] 收到 %d 个有效路径: %v", len(clean), clean)
	}

	err := wails.Run(&options.App{
		Title:            "GameTime · 游戏时长",
		Width:            1080,
		Height:           760,
		MinWidth:         900,
		MinHeight:        620,
		// 注意：Wails v2 不允许同时设置 Assets 与 AssetServer，
		// 统一走 AssetServer（v2 的推荐方式）。
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 243, G: 243, B: 243, A: 255},
		OnStartup:        mainApp.startup,
		OnShutdown:       func(ctx context.Context) {},
		Bind:             []interface{}{mainApp},
		WindowStartState: windowStartState(startHidden),
		DragAndDrop: &options.DragAndDrop{
			// 关键：开启后 Go 侧 OnFileDrop 能拿到拖入文件的绝对路径
			EnableFileDrop:     true,
			DisableWebViewDrop: false,
			CSSDropProperty:    "--wails-drop-target",
			CSSDropValue:       "drop",
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
			WebviewUserDataPath:  filepath.Join(appDataDir(), "WebView2"),
		},
		// 关闭按钮 = 隐藏到托盘，不退出进程
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			if quitting.Load() {
				return false // 真正退出
			}
			wruntime.WindowHide(ctx)
			return true
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "gametime-single-instance-v1",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				// 第二次启动：把已有窗口叫到前台
				showMainWindow()
			},
		},
	})
	if err != nil {
		log.Printf("[main] wails 运行失败: %v", err)
	}
}

func windowStartState(hidden bool) options.WindowStartState {
	if hidden {
		return options.Minimised
	}
	return options.Normal
}

// hasFlag 检查命令行里是否包含任一 flag。
func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if equalFold(a, f) {
				return true
			}
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// initLogging 把日志写文件（-H=windowsgui 没有控制台）。
func initLogging() {
	p := filepath.Join(appDataDir(), "gametime.log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
	log.Printf("===== GameTime %s 启动 =====", appVersion)
}

// shutdownApp 收尾并退出。
func shutdownApp() {
	if quitOnce {
		return
	}
	quitOnce = true
	quitting.Store(true)
	log.Printf("[main] 正在退出")
	StopMonitor()
	if mainApp != nil && mainApp.ctx != nil {
		wruntime.Quit(mainApp.ctx)
	}
	// 兜底：1 秒后强制结束（防止 Quit 未生效）
	go func() {
		time.Sleep(time.Second)
		os.Exit(0)
	}()
}
