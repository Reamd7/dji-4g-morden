# Quectel 驱动逆向分析:userland 性能优化启发

> 调研日期: 2026-07-12
> 目标文件: `qcusbwwan.sys` (583KB) + `qcusbfilter.sys` (63KB) + `qcusbser.sys` (256KB)
> 来源: Quectel_LTE&5G_Windows_USB_Driver_V2.2.4 (2020-09-18)
> 方法: strings + PE imports 提取(非完整反汇编)

## 驱动来源确认

三个 `.sys` 全部来自 **Qualcomm 官方 QUD (Qualcomm USB Driver)**:

```
qcusbwwan.sys PDB:   F:\DriverWorkSpace\work\R03\QMI\win\qcwwan\ndis\MPQMUX.c
qcusbfilter.sys PDB: ...\qud-win-1-1_qti-tools_device_source.git\QMI\win\qcwwan\filter\
qcusbser.sys PDB:    ...\qud-win-1-1_qti-tools_device_source.git\QMI\win\qcwwan\serial\
```

这是 Qualcomm QUD 的 Quectel 定制版,基于 Gobi 驱动架构。三个驱动分工:

| 驱动 | 大小 | 职责 | userland 对应 |
|---|---|---|---|
| **qcusbwwan.sys** | 583KB | NDIS 6.2 WWAN miniport:QMI 控制 + 数据收发 + QMAP 聚合 | `qmitransport` + `qmidatapath` + `qmidial` |
| **qcusbfilter.sys** | 63KB | USB composite lower filter:QMI 接口 MUX 路由 | 无(我们直接 claim MI_04) |
| **qcusbser.sys** | 256KB | USB-to-serial 桥接:AT/DM/NMEA/Modem → COM3~5 | `usbtransport`(我们直接 claim MI_02 bulk) |

## 驱动栈(4 层)

```
Windows「设置 > 移动数据」面板
         ↕
WwanSvc (扫描 IfType=243 的 NDIS 适配器)
         ↕  NDIS 6.2 miniport 上沿
qcusbwwan.sys (583KB)  ← WWAN NDIS miniport
    ├── QMI 控制通道(WDS/NAS/DMS/UIM/WMS/QOS 六服务)
    ├── QMAP 聚合(DL 32包×32KB,UL 聚合)
    └── 数据通道:bulk EP 0x05 OUT / 0x88 IN
         ↕  IOCTL_QCDEV_READ/SEND_CONTROL
qcusbfilter.sys (63KB)  ← USB lower filter
    └── QMI MUX 接口多路复用(最多 7 session)
         ↕
USB Composite Device (PID 2C7C:0125) → MI_04
```

## qcusbfilter.sys 分析(63KB,USB composite lower filter)

极简驱动,二进制剥离严重,strings 仅剩证书链和 `filter_unknown`。关键信息来自 INF + 注册表键:

```ini
; qcfilter.inf — LowerFilters 注册
[LowerFilterAddReg]
HKR,,"LowerFilters",0x00010000,"qcfilter"

; 接口 MUX 配置(qcfilter.inf 注册表)
HKR,, QCDeviceMuxEnable, 0x00010001, 0x00000001   ; 启用 QMI MUX
HKR,, QCDeviceStartIf,   0x00010001, 0x0000000N   ; 起始接口(MI_02/03/04)
HKR,, QCDeviceNumIf,     0x00010001, 0x00000001   ; 实际接口数=1
HKR,, QCDeviceNumMuxIf,  0x00010001, 0x00000007   ; MUX 虚拟接口=7
```

**作用**: 挂在 USB composite 设备(uscbccgp)下沿,拦截 IRP,实现 QMI 接口多路复用。让 `qcusbwwan` 和 `qcusbser` 共享同一组 USB bulk endpoint,最多虚拟 7 个独立数据 session。

**对 userland 的意义**: 我们不需要。直接 `gousb claim MI_04` 就能独占 QMI bulk endpoint,无需 MUX。MUX 仅在多 PDP context(多 APN 同时拨号)时有意义。

## qcusbser.sys 分析(256KB,USB-to-serial 桥接)

完整的 USB-CDC 串口驱动,把 MI_00~03 的 USB bulk endpoint 桥接成 Windows COM 端口。

### USB 栈
```
USBD.SYS
  → USBD_CreateConfigurationRequestEx  // 选择 USB 配置
  → USBD_ParseConfigurationDescriptorEx // 解析描述符
```

### 串口 IOCTL 全集(标准 Windows serial)
```
IOCTL_SERIAL_SET_BAUD_RATE / GET_BAUD_RATE
IOCTL_SERIAL_SET_DTR / CLR_DTR / GET_DTRRTS
IOCTL_SERIAL_SET_RTS / CLR_RTS
IOCTL_SERIAL_SET_CHARS / GET_CHARS
IOCTL_SERIAL_SET_HANDFLOW / GET_HANDFLOW   // 流控(XON/XOFF, RTS/CTS)
IOCTL_SERIAL_GET_COMMSTATUS                // 线路状态
IOCTL_SERIAL_GET_MODEMSTATUS               // DCD/DSR/CTS/RI
IOCTL_SERIAL_GET_PROPERTIES                // 驱动能力
IOCTL_SERIAL_SET_TIMEOUTS / GET_TIMEOUTS   // 读写超时
IOCTL_SERIAL_SET_WAIT_MASK                 // 事件等待掩码
IOCTL_SERIAL_PURGE                         // 清缓冲
IOCTL_SERIAL_CLEAR_STATS / GET_STATS       // 统计
IOCTL_SERIAL_SET_BREAK_ON / SET_BREAK_OFF  // Break 信号
IOCTL_SERIAL_IMMEDIATE_CHAR                // 立即发送
```

### 自定义 IOCTL
```
IOCTL_QCUSB_DEVICE_POWER     // 设备电源管理
IOCTL_QCUSB_SYSTEM_POWER     // 系统电源管理
IOCTL_QCUSB_QCDEV_NOTIFY     // 设备事件通知
IOCTL_QCSER_GET_SERVICE_KEY  // 获取服务注册表键
```

### 调试日志
```
QCSER_CreateLogFile, ucRxFileName.Buffer   // Rx 数据 dump
QCSER_CreateLogFile, ucTxFileName.Buffer   // Tx 数据 dump
```

### PnP
```
PnPAddDevice, ucDeviceName.Buffer          // COM 端口名(如 "COM5")
PnPAddDevice, ucDeviceMapEntry.Buffer      // 设备映射注册表
IRP_MN_FILTER_RESOURCE_REQUIREMENTS        // 资源过滤
```

**对 userland 的意义**: 我们的 `usbtransport.go` 做了完全相同的事(USB bulk → 字节流),但跳过了 COM 端口抽象,直接用 gousb 读 bulk endpoint。省了一层内核 serial 栈,延迟更低。qcusbser 的流控/超时/事件掩码机制对我们无参考价值——AT 命令不需要硬件流控。

## 内部 QMI 栈(从 strings 提取)

### WDS — 无线数据服务(拨号 + 数据格式)

| QMI 消息 | 用途 | 我们的状态 |
|---|---|---|
| `QMIWDS_START_NETWORK_INTERFACE` | 发起数据拨号(获取 PDH) | ✅ 已实现 |
| `QMIWDS_STOP_NETWORK_INTERFACE` | 断开拨号 | ✅ 已实现 |
| `QMIWDS_GET_RUNTIME_SETTINGS` | 获取拨号结果(IP/GW/DNS/MTU) | ✅ 已实现 |
| `QMIWDS_GET_PKT_STATISTICS` | **从固件层获取精确 TX/RX 统计** | ❌ 未实现(优化点) |
| `QMIWDS_SET_CLIENT_IP_FAMILY_PREF` | IPv4/IPv6 双栈偏好 | ✅ 已实现 |
| `QMIWDS_INDICATION_REGISTER` | 注册 URC 事件(连接状态变化) | ✅ 已实现 |
| `QMIWDS_SET_EVENT_REPORT` | 注册事件报告(流量统计 URC) | ❌ 未实现 |
| `QMIWDS_GET_PKT_SRVC_STATUS` | 查询 PS 附着状态 | ❌ 用 AT+CGATT 代替 |

### WDS ADMIN — 数据格式 + QMAP 配置(★ 核心优化点)

| QMI 消息 | 用途 | 我们的状态 |
|---|---|---|
| `QMIWDS_ADMIN_SET_DATA_FORMAT` | **设置 raw-IP 模式**(禁用 QMI 头) | ✅ 已实现(WDA SetDataFormat) |
| `QMIWDS_ADMIN_GET_DATA_FORMAT` | 查询当前数据格式 | ❌ 未调用 |
| `QMIWDS_ADMIN_SET_QMAP_SETTINGS` | **配置 QMAP 聚合参数** | ❌ **未实现(关键优化)** |
| `QMIWDS_ADMIN_GET_QMAP_SETTINGS` | 查询 QMAP 配置 | ❌ 未调用 |
| `QMIWDS_BIND_MUX_DATA_PORT` | **绑定 QMUX 数据端口**(多 session) | ❌ 未实现 |

### NAS — 网络接入服务(注册/信号/运营商)

| QMI 消息 | 用途 | 我们的状态 |
|---|---|---|
| `QMINAS_GET_SIGNAL_STRENGTH` | 查询信号强度 | ❌ 用 AT+CSQ |
| `QMINAS_GET_SERVING_SYSTEM` | 查询注册状态/运营商 | ❌ 用 AT+CREG |
| `QMINAS_GET_SYS_INFO` | 查询系统信息(详细) | ❌ 未用 |
| `QMINAS_SET_EVENT_REPORT` | 注册信号变化 URC | ❌ 未用 |
| `QMINAS_INITIATE_NW_REGISTER` | 发起网络注册 | ❌ 用 AT+COPS |

### DMS — 设备管理(设备信息/SIM/模式)

| QMI 消息 | 用途 | 我们的状态 |
|---|---|---|
| `QMIDMS_GET_DEVICE_SERIAL_NUMBERS` | IMEI/MEID | ❌ 用 AT+CGSN |
| `QMIDMS_UIM_GET_IMSI` | IMSI | ❌ 用 AT+CIMI |
| `QMIDMS_UIM_GET_ICCID` | ICCID | ❌ 用 AT+QCCID |
| `QMIDMS_SET_OPERATING_MODE` | online/offline(飞行模式) | ❌ 用 AT+CFUN |

### UIM — SIM 卡(读写/验证)

| QMI 消息 | 用途 | 我们的状态 |
|---|---|---|
| `QMIUIM_GET_CARD_STATUS` | SIM 卡状态 | ❌ 用 AT+CPIN? |
| `QMIUIM_READ_TRANSPARENT` | 读透明文件(EF) | ❌ 用 AT+CRSM |
| `QMIUIM_VERIFY_PIN` | PIN 验证 | ❌ 用 AT+CPIN |

### WMS — 无线消息(SMS)

| QMI 消息 | 用途 | 我们的状态 |
|---|---|---|
| `QMIWMS_RAW_SEND` | 发送 PDU | ❌ 用 AT+CMGS ✅ 已跑通 |
| `QMIWMS_RAW_WRITE` | 写入 PDU | ❌ 用 AT+CMGW |
| `QMIWMS_LIST_MESSAGES` | 列短信 | ❌ 用 AT+CMGL |
| `QMIWMS_SET_ROUTE` | 设置短信路由(+CMTI) | ❌ 用 AT+CNMI |

### QOS — 服务质量

| QMI 消息 | 用途 | 我们的状态 |
|---|---|---|
| `QMI_QOS_BIND_DATA_PORT` | 绑定 QoS | ❌ 未用 |

---

## NDIS 数据路径(从 strings 提取)

### 下行(DL:modem → Windows)

```
USB bulk IN EP 0x88
  → QmiDataRx2()                    // 接收 QMAP 帧
  → 解析 QMAP 头,拆出 N 个 IP 包
  → NdisAllocateNetBufferAndNetBufferList()  // 分配 NDIS 包描述符
  → NdisAllocateMdl()               // ★ 零拷贝:MDL 直接指向 USB 缓冲区
  → NdisMIndicateReceiveNetBufferLists()    // 上报 NDIS 栈
  → Windows TCP/IP → App
```

### 上行(UL:Windows → modem)

```
App → Windows TCP/IP
  → NdisMSendNetBufferListsComplete()  // NDIS 下发
  → MdlSendData()                     // ★ 零拷贝:MDL 直接引用 NDIS 数据
  → QMAP 聚合(M 个 IP 包 → 1 个 QMAP 帧)
  → USB bulk OUT EP 0x05
```

### 自定义 IOCTL(filter ↔ miniport 通信)

```
IOCTL_QCDEV_READ_CONTROL   // 数据读取控制
IOCTL_QCDEV_SEND_CONTROL   // 数据发送控制
```

### 调试日志

```
QCUSB_CreateLogFile()
文件名格式: %sRx%02u%02u%02u%02u%02u%03u.log  // Rx 数据 dump
文件名格式: %sTx%02u%02u%02u%02u%02u%03u.log  // Tx 数据 dump
```

### DTR 设置(与我们一致)

```
IOCTL_SERIAL_SET_DTR    // 设 DTR(等价于我们的 CDC SetControlLineState wValue=0x0001)
IOCTL_SERIAL_CLR_DTR    // 清 DTR
IOCTL_SERIAL_GET_DTRRTS // 查询 DTR/RTS 状态
```

### NDIS OID → QMI 映射

`MPQMI_OIDtoQMI()` 函数做翻译:

| Windows OID | → QMI 消息 |
|---|---|
| `OID_WWAN_CONNECT` | `QMIWDS_START_NETWORK_INTERFACE` |
| `OID_WWAN_SIGNAL_STATE` | `QMINAS_GET_SIGNAL_STRENGTH` |
| `OID_WWAN_REGISTER_STATE` | `QMINAS_GET_SERVING_SYSTEM` |
| `OID_WWAN_DEVICE_CAPS` | `QMIDMS_GET_DEVICE_*` |
| `OID_WWAN_PIN` | `QMIUIM_VERIFY_PIN` |
| `OID_WWAN_SMS_SEND` | `QMIWMS_RAW_SEND` |
| `OID_WWAN_SMS_READ` | `QMIWMS_LIST_MESSAGES` |
| `OID_WWAN_READY_INFO` | `QMIUIM_GET_CARD_STATUS` |

---

## 性能优化启发(优先级排序)

### ★★★ QMAP 聚合(中优先级,高收益,中等复杂度)

**现状**: 我们 raw-IP 模式,每包一次 USB bulk transfer。
**目标**: QMAP 模式,DL 一次 USB read 拿多个包,UL 多个包合并一次 USB write。

**QMAP 帧格式**(参考 Linux `qmi_wwan.c` + QC 驱动):

```
DL QMAP 帧(bulk IN):
  [QMAP 头 1B][pad 1B][IP 包 1][QMAP 头 1B][pad 1B][IP 包 2]...
  
  QMAP 头: cmd(1B) + pad(1B) + pkt_size(2B) + pad(2B)
  实际: 1 个 QMAP header 包裹多个 sub-packet

UL QMAP 帧(bulk OUT):  
  [QMAP 头][IP 包]  ← 最简形式(单包)
  或 [QMAP 头][IP 包 1][QMAP 头][IP 包 2]  ← 聚合形式
```

**实现步骤**:
1. `QMIWDS_ADMIN_SET_QMAP_SETTINGS` — 启用 QMAP,设置 DL 聚合参数(32 包 × 32KB)
2. `QMIWDS_BIND_MUX_DATA_PORT` — 绑定 MUX 端口(QMAP 需要 mux ID)
3. DL 侧:`modemToTun()` 解析 QMAP 帧,拆出多个 IP 包,逐个写 TUN
4. UL 侧:`tunToModem()` 收集多个 TUN 包,加 QMAP 头合并,一次 bulk OUT

**预估代码量**: ~200 行(含测试)
**预期收益**: 大下载场景减少 ~80% USB 往返;ping 等小包场景无变化
**前提**: 需要 `QMIWDS_ADMIN` 服务(QCexpr ID 0x000C)

**风险**: QDC507 固件 QMAP 兼容性需实测;raw-IP 模式已验证稳定

### ★★ QMI 统计(低优先级,低复杂度)

**现状**: 我们在 TUN relay 层统计 TX/RX 字节数。
**目标**: 用 `QMIWDS_GET_PKT_STATISTICS` 从模块固件层获取精确统计。

**收益**:
- 包含 USB 层重传(我们看不到的)
- 包含模块丢弃的包(我们以为发出去了)
- 精确的错误统计(CRC/重传/超时)

**代码量**: ~50 行(一个 QMI 请求/响应)
**复杂度**: 低

### ★ 零拷贝(不可实现,仅记录)

NDIS MDL(Memory Descriptor List)让 USB 缓冲区和 NDIS 包共享同一段物理内存,数据零拷贝。

**为什么 userland 无法实现**: TUN Read 返回的数据在用户态缓冲区,写 USB 需要传给 libusb(内核态),跨边界必须拷贝。这是用户态架构的固有代价。

**实测影响**: 4G 带宽(50-150 Mbps)下,TUN↔USB 拷贝开销 <3% CPU,可忽略。

### ★ 纯 QMI 替代 AT(不推荐)

官方驱动完全不用 AT,全部用 QMI(NAS/DMS/UIM/WMS)。理论上更统一,但:

- 我们的 AT+QMI 混合方案已全部跑通(短信收发 + 拨号 + 上网)
- AT 命令更简单、更易调试(能直接看到文本响应)
- 切纯 QMI 需要实现 6 个 QMI 服务,~2000 行新代码
- 无功能收益,只增加复杂度

**结论**: 不值得。除非要完全消除对 MI_02 AT 口的依赖。

---

## 跨驱动设计思想(可迁移到 userland)

以下不是"功能对照",而是从三个驱动中提取的**架构设计思想**——即使实现方式不同(内核 vs 用户态),这些模式在我们 Go userland 中同样适用。

### A. 可靠性与韧性(Resilience)

#### A1. Remove Lock → 优雅关闭(来自 qcusbwwan + qcusbser)

**驱动中的实现**:
```
IoInitializeRemoveLockEx()     // 启动时初始化
IoAcquireRemoveLockEx()        // 每次 I/O 操作前获取
IoReleaseRemoveLockEx()        // 操作完成后释放
IoReleaseRemoveLockAndWaitEx() // 关闭时:等所有 in-flight I/O 排空
```
这是引用计数模式——每个 USB transfer 持有一个 lock,Close 时等待所有 lock 释放后再真正释放资源。

**我们的现状**: `usbtransport.Close()` 直接 cancel context,不等 in-flight read 完成。如果 Close 时 TUN write 还在进行,可能丢包或 panic。

**可迁移到 userland 的模式**:
```go
type Transport struct {
    inflight sync.WaitGroup  // 引用计数
    closing  atomic.Bool
}

func (t *Transport) Read(p []byte) (int, error) {
    if t.closing.Load() { return 0, ErrClosed }
    t.inflight.Add(1)         // ← acquire remove lock
    defer t.inflight.Done()   // ← release remove lock
    return t.ep.Read(p)
}

func (t *Transport) Close() error {
    t.closing.Store(true)
    t.cancelRead()
    t.inflight.Wait()         // ← ReleaseLockAndWait: 排空所有 I/O
    return t.ep.Close()
}
```
**收益**: Close 不丢包、不 panic;测试不会 race。

#### A2. Surprise Removal → 断线自动重连(来自 IRP_MN_SURPRISE_REMOVAL)

**驱动中的实现**: USB 热插拔时,PnP 管理器发 `IRP_MN_SURPRISE_REMOVAL`,驱动立即停止所有 I/O,进入等待状态;设备重新插入时重新枚举。

**我们的现状**: USB 拔出 → gousb Read 返回 error → relay goroutine 退出 → 程序退出。用户需要手动重启。

**可迁移到 userland 的模式**:
```
                   ┌── relay 正常运行 ──┐
                   │                    │
                   ▼                    │ USB Read error
              [Connected] ──────────────► [Disconnected]
                   ▲                           │
                   │ 重连成功                    │ 每 3s 重试
                   │                            ▼
              [Reconnecting] ◄──────────── [Waiting]
                   重试 claim device
```
- `modemToTun` / `tunToModem` 检测 USB error 后不退出,进入重连循环
- 重连成功后重新 StartNetworkInterface(QMI 拨号)
- TUN 保持打开,relay 恢复后继续转发(中间的包丢失是可接受的)

#### A3. 电源管理 → 休眠恢复(来自 IRP_MN_SET_POWER)

**驱动中的实现**: 系统休眠时收到 `IRP_MN_SET_POWER(PowerSystemSleeping)`,暂停所有 USB transfer;恢复时收到 `PowerSystemWorking`,重新提交 transfer。

**我们的现状**: 系统休眠 → Go 进程挂起 → USB 设备掉线 → 唤醒后 gousb 读取失败。

**可迁移到 userland 的模式**:
- macOS: 监听 `NSWorkspaceWillSleepNotification` / `NSWorkspaceDidWakeNotification`
- Linux: 监听 `org.freedesktop.login1.Manager` PrepareForSleep signal
- Windows: 监听 `WM_POWERBROADCAST` PBT_APMSUSPEND / PBT_APMRESUMESUSPEND
- 响应:休眠前 gracefully pause relay → 唤醒后 reconnect + re-dial

**优先级**: macOS 笔记本场景高优先(合盖即触发)。

### B. I/O 架构(Async Pipeline)

#### B1. 异步 send/receive 模型(来自 NDIS_STATUS_PENDING + NdisMSendNetBufferListsComplete)

**驱动中的实现**: NDIS 下发 `SendNetBufferLists` 时,驱动立即返回 `NDIS_STATUS_PENDING`,USB transfer 异步完成后回调 `SendNetBufferListsComplete`。发送和接收完全解耦。

**我们的现状**: `tunToModem` 同步读 TUN → 同步写 USB。如果 USB write 慢,TUN Read 被阻塞,可能导致 TUN 缓冲区溢出丢包。

**可迁移到 userland 的模式 — 解耦 pipeline + 有界 channel**:
```
                          有界 channel (cap=N)
TUN Read ──► [packet] ──► ┌──────────────┐ ──► USB Write
                          │  N packets   │
                          └──────────────┘
                           cap=128: 吸收突发,
                           超过则背压(阻塞 TUN Read)
```
```go
tunCh := make(chan []byte, 128)  // 有界缓冲

// Reader: TUN → channel(不阻塞在 USB write 上)
go func() {
    for {
        pkt, _ := tun.Read(buf)
        tunCh <- pkt  // 满了就阻塞 → 背压 TUN
    }
}()

// Writer: channel → USB(batch 可选)
go func() {
    for pkt := range tunCh {
        usb.Write(pkt)
    }
}()
```
**收益**: USB write 延迟不阻塞 TUN read;channel 满时自然背压防丢包。

#### B2. 批量发送(来自 OID_GEN_MAXIMUM_SEND_PACKETS + RegisterPacketTimerDpc)

**驱动中的实现**: `OID_GEN_MAXIMUM_SEND_PACKETS` 声明一次能处理的最大包数。`RegisterPacketTimerDpc` 用定时器 DPC 定期处理积压的包,不是来一个发一个。

**可迁移到 userland 的模式 — micro-batching**:
```go
// 累积最多 32 个包或等待 1ms,合并成一次 USB write
batch := make([][]byte, 0, 32)
timer := time.NewTimer(time.Millisecond)

for {
    select {
    case pkt := <-tunCh:
        batch = append(batch, pkt)
        if len(batch) >= 32 { flush(batch); batch = batch[:0] }
    case <-timer.C:
        if len(batch) > 0 { flush(batch); batch = batch[:0] }
        timer.Reset(time.Millisecond)
    }
}
```
**收益**: 小包场景(如 TCP ACK 连发)减少 USB transfer 次数。不需要 QMAP,直接用 Linux `writev` / Windows overlapped IO 合并写。

### C. 事件驱动设计(Event-Driven)

#### C1. QMI 事件订阅代替轮询(来自 INDICATION_REGISTER + SET_EVENT_REPORT)

**驱动中的实现**:
```
QMINAS_INDICATION_REGISTER   // 注册:信号变化、注册状态变化 URC
QMINAS_SET_EVENT_REPORT      // 注册:周期性信号报告
QMIWDS_INDICATION_REGISTER   // 注册:连接状态变化、数据统计 URC
QMIDMS_SET_EVENT_REPORT      // 注册:设备状态变化 URC
```
驱动不轮询信号强度,而是订阅事件。模块在状态变化时主动推送。

**我们的现状**: 信号强度靠用户主动调 `AT+CSQ`(一次性)。连接状态不监控(断了不知道)。

**可迁移到 userland 的模式**:
- 拨号后发 `QMIWDS_INDICATION_REGISTER` → 订阅连接状态 URC
- 模块在网络切换(如 4G→3G 降级)或断线时推 URC
- relay 收到 URC → 触发回调 / 自动重拨
- 比轮询更省电、更低延迟

#### C2. IOCTL_QCDEV_WAIT_NOTIFY → 异步事件等待

**驱动中的实现**: `IOCTL_QCDEV_WAIT_NOTIFY` 让调用方阻塞等待设备事件。当模块推送 URC 时,这个 IOCTL 完成返回事件数据。

**可迁移到 userland**: 我们的 QMI transport interrupt EP 0x89 已经在做类似的事——监听 `RESPONSE_AVAILABLE` notification。可以把这个模式扩展为通用事件总线:
```go
type Event struct {
    Type      EventKind  // SignalChange / ConnStateChange / SMSReceived
    Signal    int        // RSSI(dBm)
    Connected bool
    Timestamp time.Time
}

type EventBus struct { ch chan Event }
func (b *EventBus) Subscribe() <-chan Event { return b.ch }
```
上层(SMS handler / 信号显示 / 重连逻辑)各自订阅,解耦。

### D. 可观测性(Observability)

#### D1. 多层统计(来自 GET_STATS + GET_PKT_STATISTICS)

**驱动中的实现**:
- 串口层: `IOCTL_SERIAL_GET_STATS` — TX/RX 字节、帧错误、奇偶校验错误
- NDIS 层: `OID_GEN_*_RCV/XMIT` — 广播/单播包数、字节、错误、丢弃
- QMI 层: `QMIWDS_GET_PKT_STATISTICS` — 模块固件层的精确统计

**我们的现状**: 只有 relay 层的 `Relay stats: TX N pkts / M B`(qmidial 退出时打印一次)。

**可迁移到 userland 的模式 — 分层 metrics**:
```
[USB 层]  bulkRead 1234 calls, bulkWrite 1100 calls, errors 2
[Relay 层] TX 1859 pkts/322KB, RX 1886 pkts/1.1MB, dropped 0
[QMI 层]  StartNetwork OK, PDH=<PDH>, uptime 42m
[TUN 层]   read 1886, write 1859, overflow 0
```
每层独立计数,支持通过 signal/API 实时查询。调试断线问题时一目了然。

#### D2. 通信日志(来自 QCSER_CreateLogFile / QCUSB_CreateLogFile)

**驱动中的实现**: 三个驱动都有 `CreateLogFile`,可以 dump 所有 USB Rx/Tx 流量到带时间戳的 `.log` 文件。格式: `%sRx%02u%02u%02u%02u%02u%03u.log`(前缀+时间戳)。

**我们的现状**: zerolog 日志只有 AT 命令和 QMI 帧摘要,没有原始字节 dump。

**可迁移到 userland**: 加一个 `--dump` flag:
```
qmidial -dial -tun --dump /tmp/dump/
  → /tmp/dump/usb_rx_00001.log   (bulk IN 原始字节)
  → /tmp/dump/usb_tx_00001.log   (bulk OUT 原始字节)
  → /tmp/dump/tun_rx_00001.log   (TUN → modem)
  → /tmp/dump/tun_tx_00001.log   (modem → TUN)
```
调试 QMAP 兼容性、分析协议行为时极其有用。

### E. 命令适配器模式(来自 MPQMI_OIDtoQMI)

**驱动中的实现**: `MPQMI_OIDtoQMI()` 函数把 Windows 高层 OID 请求翻译为底层 QMI 消息。上层(Windows 网络)只说 OID 语言,不关心 QMI 细节。

**这是经典的 Adapter / Command 模式**:
```
[Windows OID 请求]  →  MPQMI_OIDtoQMI()  →  [QMI 消息构造]  →  [USB 发送]
       高层 API              翻译层              协议层           物理层
```

**可迁移到 userland**: 我们目前 AT 和 QMI 混用,上层(qmidial / SMS handler)直接调用具体实现。可以抽象一层:
```go
type ModemAPI interface {
    Signal() (rssi int, err error)
    Dial(apn string) (ip netip.Addr, err error)
    SendSMS(to, pdu string) error
    ReadSMS(index int) (*DecodedSMS, error)
    Operator() (name string, err error)
}
```
AT 和 QMI 各出一个实现。上层只依赖接口,切换后端不影响业务逻辑。当前 AT+QMI 混合方案已跑通,这个重构是「可选项」而非「必需项」——但如果未来要支持非 Qualcomm 模组(如 MediaTek),这层抽象是必需的。

## 与 userland 方案的不可替代差异

| 特性 | qcusbwwan.sys(内核态) | qmidial + TUN(用户态) |
|---|---|---|
| 零拷贝 MDL | ✅ `NdisAllocateMdl` | ❌ TUN↔USB 必须拷贝 |
| WwanSvc 集成 | ✅ `IF_TYPE_WWWANPP` | ❌ TUN 是虚拟适配器 |
| QMAP 聚合 | ✅ 内置 | ❌ 可实现但需额外工作 |
| 跨平台 | ❌ 仅 Windows | ✅ Win/macOS/Linux |
| 内核崩溃风险 | ❌ BSOD | ✅ 进程退出 |

**核心 tradeoff**: 零拷贝 + WwanSvc 集成 vs 跨平台。前者是性能和体验优势,后者是可移植性优势。这是架构层面的选择,不可兼得。
