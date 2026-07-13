# 桌面客户端调研:Wails v3 + React + TypeScript

> 调研日期:2026-07-13
> 分支:`feature/desktop`
> 状态:**调研完成,待实施**

## 一、目标

把现有纯用户态 DJI 4G 模组能力(`dji-modem-research` 已验证的三阶段:AT 短信 + QMI 拨号 + SOCKS5/TUN 上网)包装为**跨平台桌面 GUI**(macOS + Windows),让非技术用户也能:

- 收发短信(实时 +CMTI 推送、长短信重组、SIM 存储)
- 一键 QMI 拨号(双栈 IPv4/IPv6、信号/设备信息展示)
- 启动 SOCKS5 代理(经 4G 上网,无需 admin)或 TUN 透明上网(需 admin)
- 监控设备状态(信号强度、运营商、流量统计)

**核心价值**:现有能力全部跑通且零内核驱动,缺的只是一个普通人能点的界面。

## 二、技术选型

### 为什么是 Wails(而非 Electron / Qt / 纯原生)

| 方案 | 后端 | UI | 体积 | 适配本项目 |
|---|---|---|---|---|
| **Wails v3** | **Go** | 系统 WebView + React/TS | ~10MB 单文件 | ✅ 后端 Go,直接 import 现有 `internal/` 包,零胶水 |
| Electron | Node.js | Chromium + React | ~80MB+ | ❌ 后端切 JS,现有 Go 代码要么重写要么走 sidecar |
| Qt(Go 绑定) | Go | Qt Widgets | 中 | ❌ QML/Widgets 学习成本高,UI 表现力弱 |
| Fyne/Gio | Go | 自绘 | 小 | ❌ 自绘 UI 生态弱,无 React 成熟度 |

**结论**:Wails 是唯一能让现有 Go 协议栈**原样复用**的方案——desktop 的 `main.go` 直接 `import "dji-modem-research/internal/..."`,前端 React 只做展示和编排。

### Wails v3 vs v2(选 v3)

| 维度 | v3(选) | v2 |
|---|---|---|
| 后端模型 | **Service**(生命周期 `ServiceStartup` + `app.Event.Emit` 事件) | `Bind` + `OnDomReady`(无显式生命周期) |
| 类型生成 | `wails3 generate bindings -ts`(自动 TS 定义) | 运行时绑定,类型靠手写 |
| 长任务 | Service 内 goroutine + 事件推送前端(天然适合拨号/relay) | 需手动管理 |
| 成熟度 | 较新(2024+),文档已全(3489 snippets) | 成熟稳定,社区案例多 |

**选 v3 的理由**:拨号/relay/SMS 收信都是**长时运行 + 异步事件**场景,v3 的 Service + Event 模型正好匹配。新项目无历史包袱。

### 前端:React + TypeScript + Vite

- **React**:生态成熟,组件化适合多页面(短信/拨号/代理/设备)
- **TypeScript**:Wails 自动生成 service 的 TS 定义,类型安全调用后端
- **Vite**:Wails v3 默认打包器,开发期热重载(HMR)

## 三、Wails v3 集成模型

### Service 模式(后端 → 前端)

```go
// 后端:Go struct 注册为 service,exported 方法自动暴露
type ModemService struct {
    app *application.App
}

func (s *ModemService) SendSMS(ctx context.Context, phone, msg string) error {
    // 调用现有 modem.Modem.Send
}

// 注册
app := application.New(application.Options{
    Services: []application.Service{
        application.NewService(&ModemService{}),
    },
})
```

```bash
# 生成前端 TS 绑定(类型安全)
wails3 generate bindings -ts
```

```typescript
// 前端:类型安全调用
import { SendSMS } from "./bindings/djimodem/modemservice"
await SendSMS(phone, msg)  // Promise<void>,编译期类型检查
```

### 事件推送(后端 → 前端)

```go
// 后端:短信到达 / 拨号状态 / 流量统计 推送
s.app.Event.Emit("sms:received", map[string]any{
    "sender": sender, "content": body, "ts": timestamp,
})
```

```typescript
// 前端:订阅事件
import { Events } from "@wailsio/runtime"
Events.on("sms:received", (e) => setMessages(prev => [...prev, e.data]))
```

### Go module 集成:Workspace 方案

`desktop/` 作为**独立 module**(隔离 Wails 依赖),用 `go.work` 连接上级 `dji-modem-research`,使其能 import `internal/` 包:

```
go.work  (项目根)
├── .             # dji-modem-research(含 internal/, third_party/, cmd/)
└── ./desktop     # dji-modem-research/desktop(独立 go.mod,含 wails 依赖)
```

**好处**:Wails/React 依赖不污染协议栈仓库的 `go.mod`;协议栈改动 desktop 立即可见(workspace 实时链接)。

## 四、现有 Go 能力 → Services 映射

基于现有代码的真实方法签名(`modem/*.go`、`qmitransport/`、`manager/`、`qmidatapath/`):

### DeviceService(设备 + 信号)

| Service 方法 | 现有调用 | 前端用途 |
|---|---|---|
| `ListDevices()` | `gousb` 枚举 VID 0x2c7c | 设备连接页 |
| `Connect()` | `usbtransport.Open()` (MI_02) + `modem.NewFromIO` | 建立会话 |
| `SignalMetrics()` | `modem.SignalMetrics(ctx)` (RSSI/RSRP/RSRQ) | 信号条 |
| `DeviceInfo()` | `ICCID/IMEI/IMSI/Carrier/PhoneNumber/SoftwareVersion` | 设备信息卡 |

### ModemService(短信 + AT)

| Service 方法 | 现有调用 | 事件 |
|---|---|---|
| `SendSMS(phone, msg)` | `modem.Send` (CMGS + smscodec 分段) | `sms:sent` |
| `ListSMS()` | `modem.ListStored` + `DecodeDeliver` | — |
| `DeleteSMS(index)` | `modem.DeleteStored` | — |
| `SendAT(cmd)` | `modem.SendAndWait(ctx, cmd, t)` | 控制台 |
| 收信(自动) | `SetSMSCallback` → `smscodec.Reassembler` | **`sms:received`** |
| `Ping(target)` | `modem.IcmpPing(ctx, target, t, c)` | `ping:result` |

### DialerService(QMI 拨号)

| Service 方法 | 现有调用(`cmd/qmidial` 蓝本) | 事件 |
|---|---|---|
| `Dial(apn, ipv6)` | `qmitransport.Open` → `qmi.NewClientFromTransport` → `manager.NewWithClient` → `StartCore` → `Connect` | `dial:progress` / **`dial:connected`** |
| `Connection()` | `mgr.Settings()` / `SettingsV6()` (IP/DNS/MTU/PDH) | — |
| `Hangup()` | `mgr.Stop()` / `client.Close()` / `transport.Close()` | `dial:disconnected` |

### ProxyService(数据面)

| Service 方法 | 现有调用 | 事件 |
|---|---|---|
| `StartSOCKS5(addr)` | `OpenBulkEndpoints` + `NewNetstackPacketSink` + `Bridge.Start` + `RunSOCKS5` | **`proxy:started`** |
| `StartTUN(name)` | `tun.CreateTUN` + `TUNPacketSink` + `Bridge`(需 admin) | `proxy:started` |
| `Stats()` | `bridge.Stats()` (TX/RX 包/字节) | **`stats:update`**(定时推送) |
| `Stop()` | `bridge.Stop()` / `sink.Close()` | `proxy:stopped` |

## 五、目录结构

```
dji-modem-research/
├── go.mod                      # 现有(module dji-modem-research)
├── go.work                     # 新增:连接 . + ./desktop
├── internal/                   # 现有协议栈(被 desktop import)
├── third_party/                # 现有
├── cmd/                        # 现有 CLI 工具
│
└── desktop/                    # 【新增】Wails v3 桌面客户端
    ├── go.mod                  # module dji-modem-research/desktop(含 wails v3 依赖)
    ├── main.go                 # Wails app 入口(New + 注册 Services + Run)
    ├── services/               # Go 后端 services
    │   ├── device.go           # DeviceService
    │   ├── modem.go            # ModemService
    │   ├── dialer.go           # DialerService
    │   └── proxy.go            # ProxyService
    ├── frontend/               # React + TS(Vite)
    │   ├── index.html
    │   ├── src/
    │   │   ├── main.tsx        # React 入口
    │   │   ├── App.tsx         # 路由 + 布局
    │   │   ├── pages/
    │   │   │   ├── Dashboard.tsx   # 总览(设备/信号/连接状态)
    │   │   │   ├── Sms.tsx         # 短信收发(会话列表 + 输入)
    │   │   │   ├── Dialer.tsx      # 拨号控制(APN/双栈/IP 展示)
    │   │   │   └── Proxy.tsx       # SOCKS5/TUN + 流量统计
    │   │   ├── components/     # 通用组件(信号条/状态徽章/... )
    │   │   └── bindings/       # wails3 generate bindings 产物(不入库)
    │   ├── package.json
    │   ├── tsconfig.json
    │   └── vite.config.ts
    ├── build/                  # 平台打包配置(图标/Info.plist)
    └── Taskfile.yml            # wails3 task(dev/build/package)
```

## 六、前端架构

### 页面与状态

- **Dashboard**:设备连接状态、信号强度条、当前拨号/代理状态、一键连接
- **SMS**:会话式列表(按发件人聚合)、新短信实时入列(`sms:received` 事件)、发送框 + 长短信分段提示
- **Dialer**:APN 输入、IPv4/IPv6 双栈开关、拨号/挂断按钮、连接信息(IP/DNS/MTU)、拨号进度日志
- **Proxy**:SOCKS5 监听地址、启动/停止、模式切换(SOCKS5 无需 admin / TUN 需 admin)、实时流量图表(TX/RX,`stats:update`)

### 事件流(单向数据流)

```
Go Service (goroutine)
   │ app.Event.Emit("sms:received" / "dial:connected" / "stats:update")
   ▼
Wails runtime Events
   │ Events.on(...)
   ▼
React state (useState/useReducer)
   │
   ▼
UI re-render
```

用户操作反向:React → `import { SendSMS } from bindings` → Go Service 方法 → 现有协议栈。

## 七、实施路线图(分阶段)

| 阶段 | 范围 | 验证标志 |
|---|---|---|
| **0. 脚手架** | `go.work` + `desktop/` Wails v3 React 模板 + wails3 CLI 安装 | `wails3 dev` 启动空白窗口,React HMR 工作 |
| **1. DeviceService** | USB 枚举 + AT 连接 + 信号/设备信息 | GUI 显示 IMEI/ICCID/信号强度 |
| **2. ModemService** | 短信收发 + `sms:received` 实时事件 | GUI 收发短信(复用 hardware 测试链路) |
| **3. DialerService** | QMI 拨号 + 连接信息 + 进度事件 | GUI 一键拨号,显示 IPv4/IPv6 |
| **4. ProxyService** | SOCKS5 启停 + 流量统计 + `stats:update` | GUI 启动 SOCKS5,curl 经代理上网 |
| **5. 打磨** | Dashboard 总览、错误处理、打包(macOS .app / Windows .exe) | 交付可分发单文件 |

每阶段对应现有已验证能力,GUI 只做编排 + 展示,协议层零改动。

## 八、环境准备

### wails3 CLI

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
# 验证
wails3 version
```

### 平台依赖

- **macOS**:Xcode Command Line Tools(WebView 系统自带,无额外依赖)
- **Windows**:WebView2 Runtime(Win10+ 预装;Win7/8 需装)、wintun.dll(TUN 模式,与现有 `cmd/qmidial` 一致)

### Go workspace

```bash
# 项目根
go work init .
go work use ./desktop
```

## 九、风险与注意事项

| 风险 | 影响 | 缓解 |
|---|---|---|
| Wails v3 较新 | 可能遇 API 变动坑 | v3 文档已全(3489 snippets);遇阻可查 v2 对照,核心模型相通 |
| TUN 模式需 admin | UX 打断 | 默认 SOCKS5(无需 admin);TUN 标注"需提权"并复用 `run_tun.bat` 提升逻辑 |
| WebView 兼容 | macOS WKWebView vs Windows WebView2 差异 | React 用标准 Web API,避开实验特性;两端实测 |
| 现有代码 context/生命周期 | Service 关闭需正确释放 transport/manager | 复用 `cmd/qmidial` 的 cleanup 时序(socksCancel → sink.Close → bridge.Stop → mgr.Stop → transport.Close) |
| cgo(libusb)打包 | wintun.dll/libusb-1.0.dll 需随 exe 分发 | 复用现有 `wintun_preload_windows.go` 全路径预载方案 |

## 十、结论

Wails v3 + React + TS 是包装现有 Go 协议栈的**最优且可行**路径:

1. **零重写**:现有 ~600 行新增 + 上游协议栈原样复用,Wails 只做 `import` + Service 包装
2. **模型匹配**:Service + Event 天然适合异步拨号/收信/流量统计
3. **跨平台**:macOS + Windows 单一代码库,系统 WebView 轻量
4. **分阶段可验证**:每个阶段对应已跑通的能力,GUI 化即交付

**下一步**:进入阶段 0(脚手架)——安装 wails3 CLI、建 `go.work`、`desktop/` 初始化 React 模板、跑通 `wails3 dev`。
