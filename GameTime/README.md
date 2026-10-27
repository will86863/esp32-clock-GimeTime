# GameTime · 游戏时长上位机

监控电脑上的特定程序，把「最后启动的那个」的运行时长通过局域网 UDP 每秒发给 ESP32，
由 0.96 寸 OLED 显示。**固件无需改动**，完全对齐 `SmallDesktopDisplay V1.6.8` 的现有协议。

Go + Wails v2 单文件桌面程序，前端用微软 Fluent 2 设计系统（`@fluentui/react-components` v9）。

当前版本 **v1.2.0**：按 exe 完整路径分辨同名引擎进程 + UDP 发送自愈。

---

## 快速开始

1. 双击 `GameTime.exe` 运行（首次会在托盘常驻）。
2. 「程序」页 → 点「选择程序…」，或**直接把桌面快捷方式拖进窗口**。
   - 支持 `.exe`、`.lnk`（普通快捷方式）、`.url`（Steam 游戏快捷方式）。
3. 「设置」页填 ESP32 的固定 IP（默认 `192.168.5.86`，端口 `5005`）。
4. 完事。OLED 上就会显示程序名和 `HH:MM:SS`。

**注意**：显示名只能是**纯英文**且 **≤16 字符** —— 固件字库是 ASCII，
OLED 一行只有 16 个字符宽，超了会被画出屏幕。界面会实时校验并给出提示。

---

## UDP 协议（与固件严格对齐）

发往 `<esp32_ip>:5005`，UTF-8 JSON，每秒一帧：

```json
{"running":true,"name":"Rewrite+","up":18121,"today":18121}
{"running":false,"name":"","up":0,"today":19000}
```

| 字段 | 含义 | 固件行为 |
|---|---|---|
| `running` | 是否有已添加程序在运行 | `true` 显示程序名 + 时长 |
| `name` | 显示名（ASCII ≤16） | 居中显示在 OLED 顶部 |
| `up` | **本次会话**已运行秒数 | 大字 `HH:MM:SS` |
| `today` | **今日已添加程序合计**秒数 | 仅 `running:false` 时显示为 `TODAY TOTAL` |

约束依据（读固件源码确认）：
- `char gameName[24]` → name ≤23 字节
- `oledDrawGame()` 用 `(128-len*8)/2` 居中且不截断 → 可见宽度 ≤16 字符
- `GAME_TIMEOUT_MS = 5000` → 超过 5 秒收不到数据即视为未运行
- `today` 仅在 `!running && oledTodayEn && gameTodayValid` 时显示

---

## 功能

### 添加程序的三种方式

| 入口 | 说明 |
|---|---|
| 「选择程序…」 | 系统原生文件对话框（WebView2），可选 exe/lnk/url |
| 拖入 `.exe` | 直接添加 |
| 拖入 `.lnk` | 纯二进制解析，不依赖系统代码页，中文路径也能正确读取 |
| 拖入 `.url` | 识别 `steam://rungameid/<appid>`，自动解析出游戏名与安装目录 |

### Steam 游戏识别

1. `.url` → appid（100% 可靠）
2. appid → `appmanifest_<id>.acf` 拿游戏名 + 安装目录（100% 可靠）
3. 扫安装目录列候选 exe，**显示每个候选的图标、体积与推荐理由**，由你确认
4. 兜底：读注册表 `HKCU\Software\Valve\Steam\ActiveProcess\AppId`（Steam 自己维护的当前运行游戏）

> `appmanifest` 里没有 exe 文件名，`appinfo.vdf` 里虽然有但是复杂的二进制 VDF，
> 所以不做解析 —— 改为列出候选让你选。实测 13 个已装游戏里 6 个目录下只有 1 个 exe 可自动确定。

### 统计

- 会话记录存 `%APPDATA%\GameTime\sessions.jsonl`（每行一条，追加写，崩溃最多丢最后一行）
- 今日各程序横向柱状图、近 7/30 天折线图（手写 SVG，不引图表库）
- **只记录已添加程序的运行时长，不记录空闲时间**

### 同引擎多游戏的进程分辨

大量视觉小说共用同一套引擎 exe —— 同一台机器上就同时存在
`SiglusEngine_SteamEN.exe`（Rewrite Harvest / CLANNAD SS 用）和
`RealLiveEn.exe`（CLANNAD SS / Tomoyo After 用）。

**只按进程名匹配会串味**，所以匹配身份是 **exe 完整路径**：

| 规则 | 说明 |
|---|---|
| 路径优先 | `normPath(进程完整路径) == normPath(Game.Exe)` 才命中 |
| 名字兜底 | 只有在读不到进程路径（受保护进程）时才退回按进程名匹配 |
| 去重按路径 | 添加/改名时按完整路径判重，同名不同目录的两个游戏可以共存 |
| 会话按 PID | 判断「同一个游戏重启了」用 PID，不用进程名 |

界面上同名进程会标一个 `同名 ×N` 徽章，提示它们靠路径区分、互不影响。

> 历史包袱：v1.2.0 之前只有按名字匹配，会把 CLANNAD SS 的时长记到
> Tomoyo After 头上。`tools/fix_hist_sessions.py` 可批量改判历史记录。

### 网络健壮性

UDP 发送**不用 connected socket**（`net.Dial` + `Write`），而是
`net.ListenUDP` + `WriteToUDP`。

原因：Windows 上已连接的 UDP socket 会把本地端点绑定到「当时那条路由」
上，一旦 Wi-Fi 重连/漫游、休眠唤醒或切换网卡，这个 socket 永久失效，
之后每次发送都返回 `WSAEINVAL(10022)`：
`wsasend: An invalid argument was supplied.`，且不会自愈 —— 只能重启程序。

改用不连接的 socket 后，每包由内核重新查路由选源地址；另外任何一次写失败
都会丢弃并重建 socket，网络恢复后最多一个发送间隔就能自动接上。

### 启动行为

- **开机自启**：写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，命令行带 `-minimized`
- **默认最小化**：`-minimized` 启动时直接隐藏到托盘，不弹窗
- **关闭按钮 = 隐藏到托盘**（不退出进程）
- **单实例**：命名互斥体 + Wails SingleInstanceLock，重复启动只会把已有窗口叫到前面

---

## 目录结构

```
GameTime/
├─ main.go            Wails 启动、命令行解析、资源嵌入
├─ app.go             暴露给前端的 API（绑定）
├─ protocol.go        UDP 载荷结构与实时状态
├─ config.go          配置读写 + 显示名校验（固件约束唯一入口）
├─ monitor.go         监控主循环（进程枚举 → 会话状态机 → UDP 发送）
├─ session.go         「最后启动优先」的选择逻辑 + 进程分辨（按 exe 完整路径）
├─ winproc.go         进程枚举 + 创建时间 + 完整路径（GetProcessTimes / QueryFullProcessImageName）
├─ steam.go           libraryfolders.vdf / appmanifest / RunningAppID / exe 候选
├─ shortcut.go        .lnk 纯解析、.url、文件夹拒绝
├─ icon.go            exe 图标提取（Shell32 + GetDIBits → PNG）
├─ stats.go           sessions.jsonl 读写与聚合
├─ tray.go            energye/systray 托盘
├─ autostart.go       注册表自启
├─ singleinstance.go  单实例互斥体
├─ assets/            appicon.png / app.ico / tray.ico
├─ frontend/          React 18 + Fluent v9 + Vite
│  └─ src/pages/      Overview / Games / Stats / Settings
└─ tools/gen_icons.mjs 图标生成（Tabler Icons + sharp，可复现）
```

---

## 从源码编译

本机 Go 不在 PATH，且自带 stdlib 有问题，必须用官方工具链：

```bash
export GOPATH="C:/Users/will/.workbuddy/binaries/go/gopath"
export GOPROXY="https://goproxy.cn,direct"
export GOSUMDB="sum.golang.google.cn"
export GOTOOLCHAIN="go1.26.10"
export PATH="/c/Users/will/.workbuddy/binaries/go/gopath/bin:/c/Users/will/.workbuddy/binaries/go/go/bin:$PATH"
export GOFLAGS="-mod=mod"

cd "D:/Downloads/V1.4/GameTime"
# 前端产物要先构建（二进制通过 go:embed 打包 frontend/dist）
(cd frontend && npm run build)
go build -trimpath -ldflags "-s -w -H=windowsgui" -o build/bin/GameTime.exe .
# 产物：build/bin/GameTime.exe
```

> 没用 `wails build` 也行：`main.go` 用 `//go:embed all:frontend/dist` 直接嵌入前端产物，
> 只要前端先 `npm run build`，`go build` 出来的就是完整单文件 exe（不需要 Wails CLI）。
> 版本号在 `main.go` 的 `appVersion` 和 `wails.json` 的 `productVersion`，两处要一起改。

跑测试：

```bash
go test ./...
```

覆盖：UDP 发送与自愈、路径归一化、同引擎多游戏的进程分辨。

重新生成图标（改了 `tools/gen_icons.mjs` 后）：

```bash
node tools/gen_icons.mjs
cp assets/appicon.png build/appicon.png   # wails 从这里取 exe 图标
```

> ⚠ 不要往项目根放 `rsrc.syso` —— wails 自己会生成 `GameTime-res.syso`，
> 两个同时存在会报 `too many .rsrc sections`。

---

## 数据与日志

| 路径 | 内容 |
|---|---|
| `%APPDATA%\GameTime\config.json` | 配置 |
| `%APPDATA%\GameTime\sessions.jsonl` | 会话统计 |
| `%APPDATA%\GameTime\session_active.json` | 进行中会话快照（异常退出后用于补记） |
| `%APPDATA%\GameTime\gametime.log` | 运行日志（无控制台，排查靠它） |

---

## 依赖

| 组件 | 版本 | 许可 |
|---|---|---|
| Wails | v2.14.0 | MIT |
| Fluent UI React | v9.74.9 | MIT |
| @fluentui/react-nav | v9.4.7 | MIT |
| energye/systray | v1.0.3 | Apache-2.0 |
| Tabler Icons | 3.49.0 | MIT |
| sharp（仅构建期） | 0.35.5 | Apache-2.0 |

运行时只依赖系统自带的 **WebView2**（Win10 21H2+/Win11 已预装）。
