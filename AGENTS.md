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
_.path = ["C:\\msys64\\mingw64\\bin"]
CC = "C:\\msys64\\mingw64\\bin\\gcc.exe"
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

## Go 项目结构(当前)

```
dji-modem-research/
├── AGENTS.md        # 本文件
├── .mise.toml       # mise 工具链配置
├── go.mod           # module dji-modem-research
├── main.go          # hello world
└── docs/            # 研究文档
```

- `go.mod` 模块名:`dji-modem-research`
- `main.go`:`fmt.Println("Hello, World!")`
- 运行:`mise exec -- go run main.go` → `Hello, World!`
