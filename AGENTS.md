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

**端到端里程碑达成(2026-07-12)**:`internal/usbtransport/` + `third_party/sms-gateway/modem/` 接入完成,hardware 测试 `TestHardwareModemInitializeAndCSQ` 通过。完整 AT 会话验证:
- `Initialize` 成功:AT → ATE0 → AT+CMEE=1 → AT+CPIN?(`+CPIN: READY`)→ AT+CMGF=0 → AT+CNMI=2,1 → AT+CPMS="SM"(`+CPMS: 3,50`)
- `SendAndWait("AT+CSQ")` 返回 `+CSQ: 26,99`(RSSI=26)
- 证明 USB transport 能驱动完整的 sms_gateway/modem AT 协议层(初始化 + URC 订阅 + 短信存储就绪)
- **阶段 1 核心目标达成**:纯用户态 USB → AT 协议层链路打通,无需任何厂商驱动

**短信收发验证完成(2026-07-12)**:`sms_hardware_test.go` 全套通过(hardware tag):
- 设备信息:ICCID `89000000000000000000` / IMEI `860000000000000` / 运营商 `Carrier` / 本机号 `+8613800000000` / 信号 `-61 dBm`
- 短信接收:`ListStored` + `DecodeDeliver` 读出 SIM 已存的 3 条中文短信(UCS-2 自动解码),发件人/正文/时间戳完整
- **短信发送**:`Send` 完整 CMGS 两步握手(`AT+CMGS=N` → 等 `>` → PDU+Ctrl-Z → OK)成功发出短信到 `+8613800000001`
- **readLine 修正**(上游 sms_gateway 在 USB 场景的 bug):`>` 提示符以空格结尾非 `\n`,传统串口靠短读部分返回能工作,USB CDC-AT bulk endpoint 上 `ReadString('\n')` 会把 `>` 永久卡在 bufio 缓冲区。修正:`readLine` 在部分读(无 `\n`)时,若 TrimSpace 后内容是 `>`,当一行返回。这是 USB transport 方案相对串口的必要适配。

**smscodec 升级完成(路线 B,2026-07-12,commit e73e4d9)**:PDU 编解码层从 SG 手写 `pdu.go` 升级到 vohive `smscodec`(委托 warthog618/sms,MIT)。三缺陷全部消除:GSM-7 扩展表完整(TS 23.038 §6.2.1)、长短信自动分段(`BuildSubmitTPDUs`)+ 自动重组(`Reassembler`)、16-bit ref UDH。`pdu.go` 改为 ~100 行 facade(保留 `DecodedSMS`/`ConcatInfo`/`DecodeDeliver` 对外形状,内部委托 smscodec);`Send` 改多段循环 CMGS(段间 500ms)。详见 `plans/archive/upgrade-smscodec.md`、`third_party/smscodec/AGENTS.md`。

**USB transport 方向 F 修复(2026-07-12)**:Read 从 200ms 短轮询改为**长阻塞读**(`readCtx = context.WithCancel` 无超时,运行期 0 次 `libusb_cancel_transfer`,只有 Close 时 cancel 一次)。原因:200ms 轮询在 WinUSB 上 send 时 read-cancel 撞 write transfer 偶发 segfault;判别实验证实 10× 降频(2000ms)消除崩溃,方向 F 把 cancel 降到 0 根治。配套:`readLine` 改逐字节读(不再依赖超时检测 `>`),同时对串口和 USB 工作。详见 `issue/001-gousb-close-transfer-cancel-crash.md`、`internal/usbtransport/AGENTS.md`。

**AT 命令全集对齐(2026-07-12)**:按 `plans/archive/at-commands-roadmap.md` Phase A-E 补 11 条 vohive 源码发过但 SG 没有的命令(全部带离线测试 `roadmap_test.go` + 硬件验证):Phase A 设备信息(IMSI/SoftwareVersion)、Phase B 网络注册(CSRegistration/PSAttached/DefinePDP/ListPDPs/QueryNetworkInfo)、Phase C 配置(SetFunctionLevel/SetQCFG)、Phase D SIM APDU(CSIM/ReadSIMFile/WriteSIMFile)、Phase E USSD(SendUSSD)。状态对照见 `docs/10-at-commands-alignment.md`。

**+CMTI 实时收信管道接入(2026-07-12)**:`SetSMSCallback(cb)` 启用 +CMTI 自动收信(+CMTI URC → CMGR 读 → DecodeDeliver → smscodec.Reassembler 重组 → CMGD 删除 → cb 回调)。`handleIncomingSMS` 在独立 goroutine 避免 readerLoop 死锁。Reassembler 真正接入(原计划留阶段 2,已提前完成)。

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

## 测试方案

### 测试约定(与上游 vohive-collection 对齐)

本项目继承上游 `quectel-qmi-go` / `uicc-go` 的测试风格,保证代码风格统一:

- **只用标准库 `testing`** —— 不引入 testify(上游 39 个测试文件 0 个用 testify)。断言用手写 `if got != want { t.Errorf(...) }`。
- **手写 mock,不用 mock 框架** —— 参考 `uicc-go/at/at_test.go` 的 `scriptPort` 模式(实现 `io.ReadWriteCloser`,内置预设响应脚本)。
- **table-driven 风格** —— 多用例用 `[]struct{ name string; ... }` 切片 + `t.Run(tc.name, ...)`。
- **测试文件与被测代码同包同目录** —— `foo.go` ↔ `foo_test.go`,包名相同(非 `foo_test`),便于访问未导出符号。

### 测试分层(按可测性 / 是否依赖硬件)

本项目代码必须按"是否需要真实 USB 硬件"严格分层,这是可测试性的核心:

| 层 | 依赖硬件? | 测试方式 | 示例 |
|---|---|---|---|
| **协议层**(QMI/AT/SMS PDU 编解码) | ❌ 纯计算 | 内存单测,输入 bytes → 断言解析结果 | QMUX 帧编解码、AT+CMGS PDU 编码、GSM7/UCS2 转换 |
| **Transport 接口适配层** | ❌ 用 mock | mock `io.ReadWriteCloser` / `qmiTransport` | USB endpoint 包装器、帧分片重组、超时处理 |
| **USB 物理层**(gousb/libusb 调用) | ✅ 需硬件 | 集成测试,**用 build tag 隔离** | `attest`、`usbprobe`、真实收发 |
| **上层业务**(拨号/短信收发编排) | ❌ 用 mock | 注入 mock transport,验证状态机/重连/事件分发 | 自动拨号重连、短信事件回调 |

**关键设计原则:USB 操作必须被接口包裹,绝不散落到业务代码里。** 这样 80% 的逻辑(协议栈、业务编排)可以完全离线、CI 里跑,只有最薄一层 USB 物理调用需要硬件验证。

### Build tag 隔离硬件测试

需要真实设备的测试/命令用 build tag `//go:build hardware` 标记,**不参与默认 `go test ./...`**,避免无设备环境跑红:

```go
//go:build hardware

package usbtransport

// 这里的测试需要插着 EC25 模块 + WinUSB 驱动,否则跳过或失败。
```

- 默认(CI、无硬件):`go test ./...` —— 只跑纯逻辑 + mock 测试
- 有硬件时:`go test -tags=hardware ./...` —— 额外跑设备集成测试
- `cmd/` 下的 `usbprobe` / `attest` 本身就是硬件验证工具,不在 `go test` 范围(是 `go run`)

### Transport 可测性的接口设计

`docs/01` 已确认两个核心上游接口(本项目要实现 USB 版本):

```go
// quectel-qmi-go 的 transport 接口(pkg/qmi/transport.go)
type qmiTransport interface {
    Read([]byte) (int, error)
    Write([]byte) (int, error)
    Close() error
    SetReadDeadline(time.Time) error
}

// uicc-go 的 AT 接口(at/at.go)
// Reader.port 字段类型是 io.ReadWriteCloser
```

本项目 USB transport 实现这两个接口后,**测试时注入内存版 mock 即可**,无需真实 USB。mock 直接复用上游 `scriptPort` 思路(预设响应队列 + 记录写入)。

### 跑测试的命令(均在 mise exec 下)

```bash
# 默认:纯逻辑 + mock 测试(CI 友好,无需硬件)
mise exec -- go test ./...

# 带硬件:额外跑设备集成测试(需 EC25 + WinUSB)
mise exec -- go test -tags=hardware ./...

# 详细输出 + 覆盖率
mise exec -- go test -v -cover ./...

# 单个包
mise exec -- go test -v ./internal/usbtransport/

# 竞态检测(transport/并发代码必跑)
mise exec -- go test -race ./...
```

> **`-race` 是硬性要求**:transport 层有并发读写(Read goroutine + Write 主循环),所有 transport/manager 相关测试必须通过 `-race`。

### 覆盖率目标

| 层 | 目标 | 说明 |
|---|---|---|
| 协议编解码 | ≥ 90% | 纯函数,边界用例必须覆盖 |
| Transport 适配层 | ≥ 80% | 用 mock,需覆盖分片/超时/错误路径 |
| 业务编排 | ≥ 70% | 用 mock,重点测重连/事件分发/错误恢复 |
| USB 物理层 | 不计 | 靠硬件集成测试,不追求覆盖率 |

### Pre-commit Hook(强制 go test)

`.githooks/pre-commit` 在每次 `git commit` 前自动跑 `go test -race ./internal/...`,失败则中止提交。

- **激活方式**:`git config core.hooksPath .githooks`(已对本仓库执行;新克隆后需手动执行一次)
- **绕过**:`git commit --no-verify`(不推荐)
- Hook 内部镜像 Makefile 的环境设置:`mise where go` 解析 GOROOT,从 `USERPROFILE` 推导 Windows env(go.exe 需要)
- race 检测对并发代码硬性要求,所以 hook 总是用 `-race`

## Go 项目结构(当前)

```
dji-modem-research/
├── AGENTS.md        # 本文件
├── .mise.toml       # mise 工具链配置
├── .githooks/       # pre-commit hook(强制 go test -race 通过)
├── Makefile         # 标准化 test/cover/lint 等命令
├── go.mod           # module dji-modem-research
├── main.go          # hello world
├── internal/
│   ├── usbdesc/     # USB 描述符格式化(纯逻辑,从 usbprobe 抽出,100% 覆盖)
│   ├── testutil/    # ScriptPort mock io.ReadWriteCloser(供 transport 测试)
│   └── usbtransport/# ATTransport:USB bulk endpoint → io.ReadWriteCloser
│       ├── usbtransport.go             # Open/Read(长阻塞读,方向F)/Write/Close
│       ├── usbtransport_test.go        # mock 单测(ScriptPort + modem NewFromIO 集成)
│       ├── usbtransport_hardware_test.go # AT 通路硬件集成测试(build tag: hardware)
│       └── sms_hardware_test.go        # 短信收发硬件集成测试(build tag: hardware)
├── third_party/
│   ├── sms-gateway/ # 从 sms_gateway 复制的 AT 协议层"壳"(AGPL-3.0)
│   │   ├── LICENSE
│   │   └── modem/   # SG 壳:readerLoop/SendAndWait/transport + AT 命令全集
│   │       ├── modem.go          # 并发模型 + NewFromIO + readLine(USB 适配)
│   │       ├── sms.go            # Initialize/Send(多段)/ListStored/ReadStored/设备查询/SetSMSCallback(+CMTI 管道)
│   │       ├── pdu.go            # PDU facade(~100行,委托 smscodec;保留 DecodedSMS/ConcatInfo/DecodeDeliver)
│   │       ├── network.go        # 网络注册/拨号状态(CREG/CGATT/CGDCONT/QNWINFO,Phase B)
│   │       ├── sim.go            # SIM APDU 透传(CSIM/CRSM,Phase D)
│   │       ├── config.go         # 配置/复位(CFUN/QCFG,Phase C)
│   │       ├── sms_test.go       # SG 原有 + 扩展的离线测试
│   │       ├── pdu_test.go       # facade 离线测试(GSM-7 扩展表/长短信等回归)
│   │       └── roadmap_test.go   # 11 条 AT 命令全集的离线测试(Phase A-E)
│   └── smscodec/     # 从 vohive verbatim 复制的 PDU 编解码"芯"(PolyForm NC + warthog618 MIT)
│       ├── LICENSE              # vohive PolyForm Noncommercial
│       ├── pdu.go               # DecodeDeliverTPDU/BuildSubmitTPDUs + RPDU 函数
│       ├── reassembler.go       # 长短信分片重组
│       ├── pdu_trim.go          # 国产模组 GSM-7 spare bit 清零 + PDU 长度裁剪
│       ├── binary_classifier.go # 8-bit 二进制短信分类(OMA CP/WAP/MMS,当前不验证)
│       ├── wbxml_omacp.go       # OMA CP WBXML 解码(同上)
│       └── *_test.go            # reassembler/pdu 编码/pdu_trim 离线回归向量
├── cmd/
│   ├── usbprobe/    # USB 接口/endpoint 枚举探针(硬件,go run)
│   └── attest/      # MI_02 AT 通路验证(硬件,go run)
├── issue/
│   └── 001-gousb-close-transfer-cancel-crash.md  # WinUSB cancel 崩溃判别实验(方向F 根因)
├── plans/
│   └── archive/     # 已完成的实施计划(gousb/usb-transport/upgrade-smscodec/at-commands-roadmap)
└── docs/            # 研究文档(01-03 方案/硬件/源码,04-07 AT 标准与选型,08-10 AT 命令索引与对齐)
    ├── 01-userland-usb-modem-feasibility.md
    ├── 04-at-command-standards.md       # AT 命令电信标准规范索引
    ├── 05-sms-gateway-modem-analysis.md # SG modem 剖析
    ├── 06-vohive-modem-analysis.md      # vohive modem + smscodec 剖析
    ├── 07-at-implementation-comparison.md # 三实现对比 + 选型(路线 A/B/C)
    ├── 08-ec25-at-commands-index.md     # EC25 AT 命令索引
    ├── 09-ec20-at-commands-index.md     # EC20 AT 命令索引
    ├── 10-at-commands-alignment.md      # 逐命令对齐状态(SG/VH 溯源 + 大类评分)
    ├── EC25_EC21_AT_Commands_Manual_V1.2.pdf
    └── EC20_AT_Commands_Manual.pdf
```

- `go.mod` 模块名:`dji-modem-research`
- 依赖:`github.com/google/gousb`(USB)、`github.com/rs/zerolog` + `go.bug.st/serial`(modem 包保留)、`github.com/warthog618/sms`(smscodec 用,MIT)
- 运行探针:`mise exec -- go run ./cmd/usbprobe/`
- 运行 AT 测试:`mise exec -- go run ./cmd/attest/`
- 跑 mock 单测:`make test-race`
- 跑硬件集成测试(需 EC25 + WinUSB):`make test-hardware`

### third_party 复制方案(非 replace,SG 壳 + smscodec 芯)

两个 third_party 包都从上级 `source/` **复制**(非 go.mod replace),理由:可移植(无绝对路径依赖)、可改(改副本不影响上游)、依赖最小。选型见 `docs/07`。

- **`sms-gateway/modem/`**(AGPL-3.0):AT 协议"壳"。核心改动:`port serial.Port` → `io.ReadWriteCloser` + `NewFromIO`(transport 注入);`readLine` USB 适配(`>` 提示符 + 逐字节读,方向F);`pdu.go` facade 化(委托 smscodec);补 11 条 AT 命令(network/sim/config 新文件);`SetSMSCallback` +CMTI 实时收信管道。保留 ICMP ping / zerolog / Open 串口路径。
- **`smscodec/`**(vohive PolyForm NC + warthog618 MIT):PDU 编解码"芯"。**verbatim 一行不改**,委托 warthog618/sms 完整实现 TS 23.040(GSM-7 扩展表/长短信分段/重组),加国产模组容错(pdu_trim.go)。零 vohive 内部包依赖(复制前提)。
