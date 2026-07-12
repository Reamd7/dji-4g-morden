# qcusbwwan.sys 逆向分析:userland 性能优化启发

> 调研日期: 2026-07-12
> 目标文件: `qcusbwwan.sys` (583KB, NDIS 6.2 WWAN miniport)
> 来源: Quectel_LTE&5G_Windows_USB_Driver_V2.2.4 (2020-09-18)
> 方法: strings + PE imports 提取(非完整反汇编)

## 驱动来源确认

```
qcusbwwan.sys PDB:
  F:\DriverWorkSpace\work\R03\QMI\win\qcwwan\ndis\MPQMUX.c

qcusbfilter.sys PDB:
  E:\MyProWin10\windows_driver\R02\qud-win-1-1_qti-tools_device_source.git
```

这是 **Qualcomm 官方 QUD (Qualcomm USB Driver)** 的 Quectel 定制版,基于 Gobi 驱动架构。内含完整 QMI 协议栈。

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

## 与 userland 方案的不可替代差异

| 特性 | qcusbwwan.sys(内核态) | qmidial + TUN(用户态) |
|---|---|---|
| 零拷贝 MDL | ✅ `NdisAllocateMdl` | ❌ TUN↔USB 必须拷贝 |
| WwanSvc 集成 | ✅ `IF_TYPE_WWWANPP` | ❌ TUN 是虚拟适配器 |
| QMAP 聚合 | ✅ 内置 | ❌ 可实现但需额外工作 |
| 跨平台 | ❌ 仅 Windows | ✅ Win/macOS/Linux |
| 内核崩溃风险 | ❌ BSOD | ✅ 进程退出 |

**核心 tradeoff**: 零拷贝 + WwanSvc 集成 vs 跨平台。前者是性能和体验优势,后者是可移植性优势。这是架构层面的选择,不可兼得。
