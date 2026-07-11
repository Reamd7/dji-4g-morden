# AGENTS.md — 项目长期记忆

本文件记录关于本项目的关键信息,供后续会话快速恢复上下文。

## 项目概览

`dji-modem-research` —— DJI 调制解调器研究 + 用户态驱动实现项目。

### 核心目标

写一个**纯用户态、跨平台的 DJI 4G 模组驱动**(Go),不依赖 Quectel 厂商驱动,通过 USB 直连 DJI "百望" 4G 模块(Quectel QDC507 / EG25-G),实现 **SIM 卡上网 + 短信收发**。

**为什么**:装厂商驱动 → 平台依赖(macOS 完全不支持、飞牛 NAS 需自编内核模块、闭源黑盒);切 RNDIS → 免驱但丢 AT 口、双重 NAT;虚拟机直通 → 复杂且有开销。用户态方案是唯一能让三平台(尤其 macOS)都获得完整 4G 模组能力的路径。

**技术路线**(`docs/01` 已论证):
1. 前置:Windows 用 Zadig 装 WinUSB(非厂商驱动);macOS/Linux 无需操作
2. USB transport:gousb/libusb 直连 USB bulk endpoint,替代内核 cdc-wdm + 串口驱动(~200 行)
3. 协议层:复用 `vohive-collection` 现成 Go 协议栈
   - `quectel-qmi-go`(~5000 行,QMI 全栈 + 管理器 + 三平台网络配置 netcfg)
   - `uicc-go`(~2000 行,AT + APDU,接口是 `io.ReadWriteCloser`)
   - `euicc-go`(~3000 行,eSIM LPA)
4. 虚拟网卡:wireguard/tun + QMI WDS 拨号 → 注入 TUN(~250 行)
5. 总计新增 ~600 行,80% 协议逻辑可直接复用

**关键约束**:
- 模块必须刷成**标准 EC25 PID**(2C7C:0125)。DJI 私有 PID(2CA3:4006)走私有协议,标准 QMI/AT 无效(CDC Union 描述符损坏、AT 口错位在 Iface 3)
- 选 **Go 而非 Rust**:Go 有现成协议栈;Rust 要从零写 7-9K 行(SimAdmin 绑死 ModemManager D-Bus,不可跨平台)。Rust 唯一优势 nusb(纯 Rust USB)不足以弥补

**三阶段路线图**(`docs/01` 第六节):
- 阶段 1:AT 通道 + 短信(最快验证,~200 行新代码)
- 阶段 2:QMI 通道 + 拨号(中等,~150 行)
- 阶段 3:TUN 虚拟网卡 + 上网(最难,~250 行)

### 实测验证结果(2026-07-11)

**USB endpoint 地址实测确认**(解除 `docs/01` §8.1 的未知风险)。DJI 百望 EC25 模式(PID 0125)布局:

| 接口 | 用途 | Endpoints |
|---|---|---|
| MI_00 | DM 诊断口 | EP 0x01 OUT bulk / EP 0x81 IN bulk |
| MI_01 | NMEA GPS | EP 0x02 OUT / EP 0x82 IN bulk / EP 0x83 IN intr(10B) |
| **MI_02** | **AT 命令口** | **EP 0x03 OUT bulk / EP 0x84 IN bulk / EP 0x85 IN intr(10B)** |
| MI_03 | Modem 控制 | EP 0x04 OUT / EP 0x86 IN bulk / EP 0x87 IN intr(10B) |
| **MI_04** | **QMI 数据通道** | **EP 0x05 OUT bulk / EP 0x88 IN bulk / EP 0x89 IN intr(8B)** |

规律:OUT 端点递增 0x01~0x05,IN 端点递增 0x81~0x89。MI_04 的 interrupt 端点 maxPacket=8(QMI URC 通知),其他接口的 interrupt maxPacket=10。

**AT 通路已打通**(`cmd/attest/`):通过 gousb claim MI_02 → EP 0x03 OUT 发 `AT\r\n` → EP 0x84 IN 收到 `AT\r` 回显 + `\r\nOK\r\n`。证明整条链路 gousb → libusb → WinUSB → USB bulk → 模块 AT 口工作正常。阶段 1 物理基础 OK。

### 目录结构

- `docs/` —— 调研报告(中文 markdown)
  - `00-reference-index.md` —— 参考索引,指向上级 `vohive-release/` 的教程/资产/源码
  - `01-userland-usb-modem-feasibility.md` —— 用户态 USB modem 可行性研究(核心方案文档)
  - `02-dji-modem-hardware-and-flashing.md` —— DJI 模块硬件分析 + 刷写研究
  - `03-source-code-analysis.md` —— VoHive 生态源码深度分析(协议栈复用性 + 跨平台评估)
- Go 项目(2026-07-11 创建)—— 用户态 USB modem 程序的实现代码,当前为 hello world 骨架。
- 上级 `vohive-release/` 含教程、二进制资产、`source/` 下 5 个源码仓库(协议栈复用来源)

## 工具链管理(mise)

本项目用 **mise** 管理 Go 与工具链环境,配置在 `.mise.toml`。

### `.mise.toml` 当前内容

```toml
[tools]
go = "latest"

[env]
GOPATH = "{{ config_root }}/.gopath"
# Force `go install` into GOPATH/bin (overriding mise's GOBIN default that
# points into the Go install dir, which gets wiped on version bumps).
GOBIN = ""
# _.path order matters: .gopath/bin first (so gopls/dlv/staticcheck resolve
# before anything the parent shell injects), then mingw64/bin for gcc.
_.path = [
    "{{ config_root }}/.gopath/bin",
    "C:\\msys64\\mingw64\\bin",
]
CC = "C:\\msys64\\mingw64\\bin\\gcc.exe"
PKG_CONFIG_PATH = "C:\\msys64\\mingw64\\lib\\pkgconfig"
```

### 关键约定 / 踩坑记录

- **Go 版本**:`go = "latest"`,截至 2026-07-11 解析为 go1.26.1 windows/amd64。
- **mise 信任**:新建/修改 `.mise.toml` 后需先 `mise trust` 才能 `mise install` / `mise exec`。
- **运行 Go 命令**:统一用 `mise exec -- go <cmd>`(或在已激活 mise 的 shell 中直接 `go <cmd>`)。
- **mingw64 gcc 接入**:为支持 CGO(如 SQLite 驱动),接入 `C:\msys64\mingw64\bin\gcc.exe`(16.1.0)。
  - `_.path` 把 mingw64/bin 前置到 PATH。
  - **`CC` 必须显式锁定为绝对路径**:git bash 会把 MSYS2 的 `/c/msys64/usr/bin/gcc`(15.2.0)注入 PATH 且排在 `_.path` 前面,导致 `which gcc` 命中的是错的那个。设置 `CC` 后 CGO 编译器确定,不受 PATH 顺序干扰。
  - `CGO_ENABLED=1` 默认开启。
  - 已用最小 CGO 程序验证通过。
- **libusb-1.0 + pkg-config**(gousb USB 通信必需):
  - 通过 `pacman -S mingw-w64-x86_64-libusb mingw-w64-x86_64-pkg-config` 安装(MSYS2)。
  - gousb 是 cgo 绑定 libusb,编译时 `pkg-config --cflags --libs libusb-1.0` 必须能找到。
  - **`PKG_CONFIG_PATH` 必须显式设为 `C:\msys64\mingw64\lib\pkgconfig`**,否则 cgo 报 `pkg-config: executable file not found` 或找不到 libusb。
  - libusb-1.0.dll 运行时需要在 PATH 里(mingw64/bin 已通过 `_.path` 加入)。

### Go LSP 工具链

通过 `go install` 装入项目内 `GOPATH/bin`(即 `.gopath/bin/`),不入库(`.gitignore` 已忽略 `/.gopath/`)。

| 工具 | 版本(2026-07-11) | 用途 |
|---|---|---|
| **gopls** | v0.23.0 | Go 官方 LSP server(代码补全/跳转/诊断) |
| **dlv** (Delve) | v1.27.0 | 调试器(DAP 协议) |
| **staticcheck** | 2026.1 (v0.7.0) | 静态分析 |

安装命令(均需 `mise exec` 环境下执行):
```bash
mise exec -- go install golang.org/x/tools/gopls@latest
mise exec -- go install github.com/go-delve/delve/cmd/dlv@latest
mise exec -- go install honnef.co/go/tools/cmd/staticcheck@latest
```

**踩坑 — `GOBIN` 必须显式清空**:
mise 的 Go 包默认把 `GOBIN` 指向 mise 的 Go 安装目录(`…/mise/installs/go/<ver>/bin`)。`go install` 时 `GOBIN` 优先级高于 `GOPATH/bin`,导致工具被装进 Go 安装目录——升级 Go 版本时会被清空。`.mise.toml` 中 `GOBIN = ""` 强制落回 `GOPATH/bin`(即项目内 `.gopath/bin/`)。

**踩坑 — GOPATH 隔离**:
`GOPATH` 设为 `{{ config_root }}/.gopath`(项目内),工具不污染全局 `~/go`。`.gopath/bin` 已通过 `_.path` 加入 PATH,shell 和编辑器都能直接调用 `gopls.exe` / `dlv.exe` / `staticcheck.exe`。

### 编辑器 LSP 接入

编辑器(VSCode/ZCode 等)的 Go 插件需要知道 Go/gopls 的位置。GOROOT/GOPATH 由 mise 动态给出:
```bash
mise exec -- go env GOROOT GOPATH
```

**方式 A(推荐)— 让编辑器继承 mise 环境**:从已激活 mise 的终端启动编辑器(如 `mise exec -- code .`),或用 mise 的 shell hook。编辑器的 Go 插件会自动用 PATH 里的 `gopls`。

**方式 B(手动)— 在 settings.json 指定**:
```jsonc
{
  // VSCode / ZCode settings.json
  "go.goroot": "<mise exec -- go env GOROOT 的输出>",
  "go.gopath": "C:\\Users\\reamd\\Documents\\experiment_area\\vohive-release\\dji-modem-research\\.gopath",
  "go.useLanguageServer": true,
  "go.toolsManagement.autoUpdate": false  // 工具由 mise 管,别让插件自己装
}
```
> 注意设 `go.toolsManagement.autoUpdate: false`,否则 Go 插件会把工具又装回全局 `~/go`,与 mise 管理的版本冲突。

## Go 项目结构(当前)

```
dji-modem-research/
├── AGENTS.md        # 本文件
├── .mise.toml       # mise 工具链配置
├── go.mod           # module dji-modem-research
├── main.go          # hello world
├── cmd/
│   ├── usbprobe/    # USB 接口/endpoint 枚举探针
│   └── attest/      # MI_02 AT 通路验证(发 AT 收 OK)
└── docs/            # 研究文档
```

- `go.mod` 模块名:`dji-modem-research`,依赖 `github.com/google/gousb`
- 运行探针:`mise exec -- go run ./cmd/usbprobe/`
- 运行 AT 测试:`mise exec -- go run ./cmd/attest/`
