# 阶段 2 实施计划:QMI 通道 + 数据拨号

> 本文件是**设计文档**,记录用 gousb 直连 DJI 百望模组 MI_04(QMI 数据通道),接入 quectel-qmi-go 协议栈实现 QMI 拨号的方案。
> 创建于 2026-07-12。对应 `docs/01` 第六节"阶段 2"。前置:阶段 1 已全部完成(USB transport + AT + 短信 + smscodec 升级 + AT 命令全集,见上级 `AGENTS.md` 实测记录)。
> 决策依据:本计划 Phase 0 的调研结果(两份 Explore agent 报告,已纳入下文)。

---

## 一、目标与挑战

### 目标
通过 USB 直连 MI_04 的 QMI 通道,用 quectel-qmi-go 的 WDS/WDA service 发起数据拨号,拿到运营商分配的 IP/MTU/DNS。这是 `docs/01` 阶段 2 的核心。

### 阶段 2 不做的事(留给阶段 3)
- **不创建 TUN 虚拟网卡**(阶段 3:wireguard/tun)
- **不把 IP 包注入系统网络栈**(阶段 3:QMI bulk ↔ TUN 桥接)
- **不做实际上网验证**(阶段 3:pings/curl)

阶段 2 的成功标准是:**WDS StartNetworkInterface 成功 + GetRuntimeSettings 拿到有效 IP/MTU/DNS**。拿到 IP 后怎么用是阶段 3 的事。

### 与阶段 1 的关键差异(复杂度来源)

| 维度 | 阶段 1(AT + 短信) | 阶段 2(QMI + 拨号) |
|---|---|---|
| endpoint 数 | 2(bulk IN/OUT) | 3(bulk IN/OUT **+ interrupt IN**) |
| 传输模型 | 确定(字节流 + 行解析) | **未知**(bulk 裸 QMUX vs EP0 控制封装,Phase 0 探测) |
| transport 接口 | `io.ReadWriteCloser`(简单) | `qmiTransport`(Read/Write/Close/**SetReadDeadline**) |
| 协议层注入点 | `modem.NewFromIO` 已导出 | **未导出**,必须改 quectel-qmi-go 源码 |
| 帧封装 | 裸 AT 命令 + `\r\n` | QMUX 帧(6字节头 + SDU + TLV) |
| 上层编排 | modem 包(轻) | manager 包(重:拨号/重连/modem-reset/netcfg) |

---

## 二、关键发现(Phase 0 调研结论)

### 2.1 QMI 在 USB 上有两种传输模型(必须 Phase 0 实测决定)

EC25 MI_04 是"3端点合一"接口(bulk IN + bulk OUT + interrupt IN),**固件两种模型都支持**:

| 模型 | TX(主机→模组) | RX(模组→主机) | 复杂度 | 谁用 |
|---|---|---|---|---|
| **A. bulk 裸 QMUX**(GobiNet 风格) | EP 0x05 bulk OUT 发裸 QMUX 帧 | EP 0x88 bulk IN 收裸 QMUX 帧 | **低**(单通道读写) | Quectel GobiNet 厂商驱动 |
| **B. EP0 控制封装**(cdc-wdm/qmi_wwan 风格) | Device.Control 发 `SEND_ENCAPSULATED_COMMAND`(bmRequestType=0x21, bRequest=0x00) | 等 interrupt 0x89 的 `RESPONSE_AVAILABLE` 通知 → Device.Control 发 `GET_ENCAPSULATED_RESPONSE`(0xA1/0x01)拉取 | **高**(拼 setup packet + interrupt 同步) | Linux 主线 qmi_wwan 驱动 |

**证据**:
- Linux `qmi_wwan.c` 注释明说"Some devices combine the 'control' and 'data' functions into a single interface with all three endpoints"——EC25 正是此布局,两种驱动都能绑
- `quectel-qmi-proxy.c` 用户态写裸 QMUX 到 `/dev/cdc-wdm`,内核 cdc-wdm 驱动再封装成 EP0 控制传输(模型 B);GobiNet 直接在 bulk 上收发(模型 A)
- usbmon 抓包(Dawid Wróbel 文章)实证了 EP0 封装模型:interrupt 是 8 字节 CDC notification(不含数据),响应体要再发控制传输取

**为什么不能直接选**:`docs/01` §8.1 的同款风险——社区经验说两条都通,但**没在我们的 PID 0x0125 上实证过**。猜错模型 = transport 写得再对也收不到响应。所以 Phase 0 先探针。

### 2.2 interrupt EP 0x89 的作用(两种模型下都可先忽略)

- **cdc-wdm 模型**:interrupt 承载 8 字节 CDC notification("有数据可读"),响应体不在 interrupt 上。是延迟优化,非关键控制。
- **bulk 模型**:GobiNet 不用 interrupt,QMI 响应/indication 全走 bulk IN。
- **quectel-qmi-go 的 readLoop** 是单通道轮询(100ms ReadDeadline),所有 indication 都从一条流解析——已证明"不读 interrupt 也能收齐"。

**结论**:Phase 0 探针 + 阶段 2 实现都**先忽略 EP 0x89**。只读 bulk IN(模型 A)或轮询 GET_ENCAPSULATED_RESPONSE(模型 B)。如果将来出现"某些 indication 收不到/收晚了",再把 0x89 当 wake-up 信号接上(8字节读出丢弃即可)。

### 2.3 transport 注入 quectel-qmi-go 必须改源码

调研确认:
- `pkg/qmi/transport.go:16` 的 `qmiTransport` 接口**未导出**(小写)
- `openRawTransportHook` / `openProxyTransportHook`(transport.go:31-34)**未导出包级 var**,Go 规则:外部包无法赋值
- `newClientWithTransport`(client.go:371)**未导出**
- 唯一导出入口 `NewClientWithOptions`(client.go:244)强走 hook,无传 transport 参数

**结论**:要从外部注入 USB transport,**必须改 quectel-qmi-go 源码**导出注入点。最小改动:导出 `NewClientWithTransport`(或把两个 hook 改大写 + 加 setter)。

### 2.4 manager 包能力极强(改 qmi 源码后全部复用)

`pkg/manager/` 自动处理:
- 拨号:CTL sync → WDA SetDataFormat(raw-IP)→ WDS StartNetworkInterface → GetRuntimeSettings
- 自动重连(modem-reset 恢复、retry 退避、indication 触发的状态检查)
- QMAP 多 PDN(BindMuxDataPort)
- 网卡配置(netcfg 三平台:SetIPAddress/AddDefaultRoute/SetMTU/UpdateDNS)
- manager 唯一开设备的点:`manager.go:3017` 调 `qmi.NewClientWithOptions(... ControlPath ...)`——走 hook

**注入策略**:在 qmi 层导出注入点后,manager **完全不动**,自动受益(hook 被替换 → manager 的全部能力复用)。`Config.Device.ControlPath` 填占位字符串、`Config.ClientOptions.UseProxy=false`。

### 2.5 QMUX 帧结构(用户态写的是裸 QMUX)

无论模型 A/B,**用户态读写的就是裸 QMUX 帧**:
```
[1字节 IFType=0x01] [2字节 Length LE16] [1字节 ControlFlags] [1字节 ServiceType] [1字节 ClientID]
+ SDU 头(CTL 6字节 / 其他 service 7字节) + TLV(Type/Length LE16/Value)
```
- 模型 A:直接在 bulk 上发/收这个字节流
- 模型 B:这个字节流作为 payload 塞进 EP0 控制传输

quectel-qmi-go 的 `readLoop` 用 `pending[0]==0x01` 找帧起点,`1+LE16(pending[1:3])` 算帧长,`UnmarshalPacket` 解析。transport 层不加额外封装。

### 2.6 WDA SetDataFormat 时机(影响阶段 3 数据通路)

- 拨号**之前**先调 WDA SetDataFormat(raw-IP):`manager.go` 的 `ensureDataPlaneServices` → `enableRawIP`
- `LinkProtocolIP=0x02`(raw-IP)vs `LinkProtocolEthernet=0x01`(802.3)
- **QMAP 模式**(TLV 0x12/0x13=0x05):数据包前有 **4 字节** QMAP 头(flags/mux_id/pkt_len),阶段 3 要剥/加头
- **纯 raw-IP**:数据包裸 IP,0 字节头

阶段 2(信令)不受影响;阶段 3(数据通路)需要检测协商结果条件性剥头。

---

## 三、Phase 0 — QMI 传输模型探针(必须先做)

### 3.1 目标
在真实 EC25 PID 0x0125 上实测:发 QMICTL_SYNC_REQ 到 bulk OUT 和 EP0,看哪个回 SYNC_RESP。用事实决定走模型 A 还是 B。解除 `docs/01` 的传输模型未知风险。

### 3.2 探针程序 `cmd/qmiprobe/main.go`(~100 行)

**QMICTL_SYNC_REQ 帧构造**(CTL service, msg 0x0027):
```
01          IFType
00 28       Length LE16 = 40 (0x0028)
00          ControlFlags
00          ServiceType = CTL
00          ClientID = 0(CTL 广播)
00 00 00    CTL: ControlFlags + TxID(1字节)=0
27 00       MsgID LE16 = 0x0027 (SYNC)
00 00       Length LE16 = 0
```
共 13 字节(SYNC_REQ 无 TLV)。完整字节:`01 00 28 00 00 00 00 00 00 27 00 00 00`

**两路探测**:

```go
func main() {
    ctx := gousb.NewContext()
    dev, _ := ctx.OpenDeviceWithVIDPID(0x2C7C, 0x0125)
    cfg, _ := dev.Config(1)
    iface, _ := cfg.Interface(4, 0)  // MI_04

    syncReq := []byte{0x01, 0x00, 0x28, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x27, 0x00, 0x00, 0x00}

    // 探测 A: bulk 模型
    bulkOut, _ := iface.OutEndpoint(0x05)
    bulkIn, _ := iface.InEndpoint(0x88)
    bulkOut.Write(syncReq)
    buf := make([]byte, 512)
    rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    n, err := bulkIn.ReadContext(rctx, buf)
    fmt.Printf("[bulk A] n=%d err=%v bytes=% x\n", n, err, buf[:n])

    // 探测 B: EP0 控制封装
    // SEND_ENCAPSULATED_COMMAND: bmRequestType=0x21, bRequest=0x00, wValue=0, wIndex=4(接口号)
    dev.Control(0x21, 0x00, 0x0000, 4, syncReq)
    time.Sleep(500 * time.Millisecond)
    // GET_ENCAPSULATED_RESPONSE: bmRequestType=0xA1, bRequest=0x01
    respBuf := make([]byte, 512)
    rctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
    n2, err2 := dev.ControlContext(rctx2, 0xA1, 0x01, 0x0000, 4, respBuf)
    fmt.Printf("[ep0 B] n=%d err=%v bytes=% x\n", n2, err2, respBuf[:n2])
}
```

**判断标准**:
- bulk A 收到以 `01 00 xx 00 01 00`(IFType + CTL + ServiceType=CTL indication 或 response)开头的字节 → **模型 A 成立**
- ep0 B 的 GET_ENCAPSULATED_RESPONSE 返回非空 QMUX → **模型 B 成立**
- 可能两个都通(社区经验如此)——选 **A**(简单)

### 3.3 探针输出记录
探针结果写入 `AGENTS.md`(解除风险) + 本计划 §3.4 决策。如果两个都通,选 bulk 模型 A,理由:架构最简单、与 quectel-qmi-go 单通道 readLoop 天然匹配、interrupt 直接忽略。

---

## 四、Phase 1 — 复制 quectel-qmi-go + 改源码导出注入点

### 4.1 复制范围

复制 `source/vohive-collection/quectel-qmi-go/` 的三个子包到 `third_party/quectel-qmi-go/`(同 smscodec 复制模式):
- `pkg/qmi/` —— QMI 协议层(QMUX 帧、CTL/WDS/WDA/NAS/DMS/UIM/WMS/IMS/VOICE service、Client、readLoop)
- `pkg/manager/` —— 高层管理器(拨号/重连/modem-reset/QMAP/事件分发)
- `pkg/netcfg/` —— 三平台网络配置(darwin/linux/windows 的 Configurator)

**不复制**:`cmd/`(cm/dms-tool 等命令行工具,我们自己写)。LICENSE 随复制(vohive 的许可)。

包路径:`dji-modem-research/third_party/quectel-qmi-go/pkg/qmi` 等。复制后 import 全部指向本仓。

### 4.2 改源码导出注入点(最小改动)

在 `pkg/qmi/transport.go` 或 `client.go` 加导出构造函数:

```go
// NewClientWithTransport constructs a Client from a pre-built transport,
// bypassing the os.OpenFile / proxy path. Enables userland USB transports
// (e.g. internal/qmitransport) to feed raw QMUX bytes into the protocol stack.
//
// The transport must implement Read/Write/Close/SetReadDeadline (QMITransport).
// path is informational (used in logs/errors only).
func NewClientWithTransport(path string, t QMITransport, opts ClientOptions) (*Client, error) {
    initCtx, cancel := context.WithTimeout(context.Background(), opts.InitTimeout)
    defer cancel()
    return newClientWithTransport(path, opts, t, initCtx), nil
}

// QMITransport is the exported interface for transport injection.
// Implement it with a USB bulk endpoint wrapper (internal/qmitransport).
type QMITransport interface {
    Read([]byte) (int, error)
    Write([]byte) (int, error)
    Close() error
    SetReadDeadline(time.Time) error
}
```

注意:`qmiTransport`(小写)和 `QMITransport`(大写)并存——内部仍用小写,导出的大写让外部实现。`newClientWithTransport` 已存在(client.go:371),只需加导出包装。

### 4.3 验证编译

`mise exec -- go build ./third_party/quectel-qmi-go/...` 通过。确认没有 vohive 内部包依赖(qmi/manager/netcfg 应该只依赖标准库 + 可能的日志库)。

---

## 五、Phase 2 — 写 QMI USB transport

### 5.1 位置:`internal/qmitransport/`(对齐 usbtransport)

```
internal/qmitransport/
├── qmitransport.go               # QMITransport 实现(Open/Read/Write/Close/SetReadDeadline)
├── qmitransport_test.go           # mock 单测(testutil.ScriptPort)
└── qmitransport_hardware_test.go  # 硬件测试(build tag: hardware)
```

### 5.2 结构体(取决于 Phase 0 结果)

**如果 Phase 0 选模型 A(bulk,倾向)**:
```go
type QMITransport struct {
    ctx     *gousb.Context
    dev     *gousb.Device
    cfg     *gousb.Config
    iface   *gousb.Interface
    bulkOut *gousb.OutEndpoint  // EP 0x05
    bulkIn  *gousb.InEndpoint   // EP 0x88
    // EP 0x89 interrupt 忽略(Phase 0 验证可忽略)
    readDeadline time.Time
    mu     sync.Mutex
    closed bool
}
```
实现:`Read`(ReadContext 长阻塞或短轮询,见下)、`Write`(bulkOut.Write)、`Close`、`SetReadDeadline`(存时间点)。

**如果 Phase 0 选模型 B(EP0 封装)**:
```go
type QMITransport struct {
    dev   *gousb.Device
    iface int  // 4
    // Write: dev.Control(0x21,0x00,0,4,payload)
    // Read:  dev.ControlContext(0xA1,0x01,0,4,buf) 轮询
}
```

**倾向模型 A**——实现和 usbtransport 几乎对称,可大量复用模式。

### 5.3 Read 的超时语义(关键,对齐 readLoop + issue/001)

quectel-qmi-go 的 `readLoop`(client.go:536)用 `SetReadDeadline` 做轮询唤醒:
- `DefaultClientOptions`(client.go:89)的 readDeadline ~100ms
- 每次 Read 用 deadline 控制阻塞时长,超时返回后 readLoop 检查 closeCh、再读

**问题**:阶段 1 的方向F 修复发现 200ms 短轮询在 WinUSB 上 `libusb_cancel_transfer` 撞 write 会 segfault(issue/001)。QMI transport 同样是 bulk + send/read 并发,**同样的崩溃风险**。

**两个选择**(Phase 2 实现时验证):
1. **长阻塞读 + Close cancel**(同 usbtransport 方向F):Read 用 `context.WithCancel` 永久阻塞,Close 时 cancel。但 quectel-qmi-go 的 readLoop **依赖 SetReadDeadline 轮询**——它每 100ms 要唤醒检查 closeCh。如果 Read 永久阻塞,closeCh 检查不到。需要要么改 readLoop(让它 select closeCh 而非靠 Read 超时),要么...
2. **长 deadline 轮询**(折中):SetReadDeadline 设 2000ms(10× 原值,issue/001 的判别实验证实 2000ms 消除崩溃),而不是 100ms。Read 用 `context.WithTimeout(2s)`。readLoop 每 2s 唤醒一次,响应略慢但够用(AT 命令通道分开,QMI 信令不是低延迟场景)。

**倾向方案 2**(长 deadline 2000ms):不动 quectel-qmi-go 的 readLoop,只调 ClientOptions 的 readDeadline 到 2000ms。如果实测 2000ms 还崩,再上方案 1 改 readLoop。

### 5.4 mock 单测(同 usbtransport 模式)

`endpointReader`/`endpointWriter` 接口抽象,`scriptReader`/`scriptWriter` 适配 `testutil.ScriptPort`。测 Read/Write/Close/SetReadDeadline/并发 + QMUX 帧往返(`qmi.NewClientWithTransport` 注入 mock transport,发 SYNC 收 SYNC_RESP)。

---

## 六、Phase 3 — WDA + WDS 拨号验证

### 6.1 manager 接入

```go
func main() {
    // 1. 打开 QMI transport(Phase 2)
    t, _ := qmitransport.Open(0x2C7C, 0x0125, 4, 0x05, 0x88)
    defer t.Close()

    // 2. 注入 quectel-qmi-go(Phase 1 的 NewClientWithTransport)
    client, _ := qmi.NewClientWithTransport("usb", t, qmi.ClientOptions{
        ReadDeadline: 2000*time.Millisecond,  // 长轮询防 issue/001
    })

    // 3. manager 接管(自动 CTL/WDA/WDS 拨号 + IP 获取)
    mgr, _ := manager.New(manager.Config{
        Client: client,  // 或通过 hook
        Device: manager.DeviceConfig{ APN: "cmnet", Auth: ... },
        NoDial: false,
    })
    mgr.Start(ctx)

    // 4. 等拨号完成,打印 IP/MTU/DNS
    // manager 的 StateConnected 事件 → GetRuntimeSettings → IP
}
```

**注意**:manager.New 现在的签名可能需要小改(它当前内部调 NewClientWithOptions 走 hook)。Phase 1 导出 NewClientWithTransport 后,可能要给 manager 也加一个"接受预构造 client"的构造路径,或让 manager 的 `openClientAndAllocateServices` 用我们注入的 client。Phase 1/3 衔接时确认。

### 6.2 验证命令

```bash
# 硬件测试(需 EC25 + WinUSB on MI_04)
DJI_TEST_APN=cmnet mise exec -- go test -tags=hardware -v -run TestHardwareQMIDial ./internal/qmitransport/
```

**成功标准**:
- `WDS StartNetworkInterface` 返回 packet data handle(非零)
- `WDS GetRuntimeSettings` 返回有效 IPv4 地址 + MTU + DNS
- 证明 USB transport 能驱动完整的 quectel-qmi-go QMI 协议层 + manager 拨号编排

---

## 七、Phase 4 — 文档 + 提交

- 更新 `AGENTS.md`:实测记录(QMI 模型确认 + 拨号结果 + IP)
- 新增 `third_party/quectel-qmi-go/AGENTS.md` + `internal/qmitransport/AGENTS.md`
- 更新 `docs/01`:阶段 2 §8.x 风险解除
- git commit

---

## 八、风险点

### 1. QMI 传输模型未知(Phase 0 解除)
`docs/01` §8.1 的同类风险。Phase 0 探针实测,不猜。

### 2. WinUSB cancel 崩溃(issue/001 在 QMI 场景的重现)
200ms 短轮询的 libusb_cancel_transfer 在 send/read 并发时 segfault。QMI transport 同样是 bulk + 并发。**缓解**:Read 用 2000ms 长 deadline(issue/001 判别实验证实的降频阈值),或长阻塞读 + 改 readLoop。Phase 2 实测。

### 3. QMAP 数据格式(影响阶段 3,不影响阶段 2)
WDA SetDataFormat 协商成 QMAP 后,数据包有 4 字节头。阶段 2(信令)不受影响;阶段 3(数据通路)要剥/加头。Phase 3 验证时记录协商结果。

### 4. manager 接入路径
manager 当前内部调 NewClientWithOptions 走 hook。Phase 1 导出注入点后,manager 可能需要小改才能接受预构造 client(或让 hook 被替换后 manager 自动受益)。Phase 1/3 衔接确认。

### 5. 模组状态
阶段 1 的 AT 通道(MI_02)和阶段 2 的 QMI 通道(MI_04)是不同接口,可同时 claim。但拨号会改变模组状态(PS attach、PDP 激活),确保拨号前 SIM ready。manager 自己会做 ensureSIMReady,但如果手动编排要注意。

---

## 九、不做的事(边界)

- 不创建 TUN 虚拟网卡(阶段 3)
- 不做 QMI 数据通路桥接(QMI bulk ↔ TUN,阶段 3)
- 不做实际上网(ping/curl,阶段 3)
- 不处理 QMAP 多 PDN(单 PDN raw-IP 先跑通)
- 不做 macOS/Linux 验证(本机 Windows 优先;跨平台 manager+netcfg 已就位,后续验证)

---

## 十、相关文件

| 文件 | 作用 |
|---|---|
| `source/vohive-collection/quectel-qmi-go/pkg/qmi/transport.go` | 源——qmiTransport 接口 + openRawTransport |
| `source/vohive-collection/quectel-qmi-go/pkg/qmi/client.go` | 源——NewClientWithOptions + newClientWithTransport + readLoop |
| `source/vohive-collection/quectel-qmi-go/pkg/manager/manager.go` | 源——拨号编排 + 重连 + netcfg |
| `source/vohive-collection/quectel-qmi-go/pkg/netcfg/` | 源——三平台网络配置 |
| `internal/usbtransport/` | 阶段 1 的 AT transport(参考模式:接口抽象 + 长阻塞读 + mock 测试) |
| `issue/001-gousb-close-transfer-cancel-crash.md` | WinUSB cancel 崩溃判别(方向F,Phase 2 Read 策略依据) |
| `docs/01-userland-usb-modem-feasibility.md` §六阶段2 + §八风险 | 原始路线图 |
| `third_party/quectel-qmi-go/` | **本计划新增**(复制 + 改源码) |
| `internal/qmitransport/` | **本计划新增**(QMI USB transport) |
| `cmd/qmiprobe/main.go` | **本计划新增**(Phase 0 探针) |

---

## 十一、实测数据参考(来自 AGENTS.md)

DJI 百望 EC25 模式(PID 0125)MI_04 QMI 通道:
- EP **0x05** OUT bulk(maxPacket=512)
- EP **0x88** IN bulk(maxPacket=512)
- EP **0x89** IN interrupt(maxPacket=8)—— QMI URC 通知,可先忽略

规律:OUT 端点递增 0x01~0x05,IN 端点递增 0x81~0x89。
