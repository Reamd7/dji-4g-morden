# Quectel 官方驱动如何让模块出现在「设置 > 移动数据」中

> 调研日期: 2026-07-12
> 驱动版本: Quectel_LTE&5G_Windows_USB_Driver_V2.2.4 (2020-09-18)
> 设备: QDC507 PID 2C7C:0125, 固件 QDC507GLEFM21

## 一句话结论

**不是靠 MBIM。** Quectel 驱动用一个**自定义 NDIS WWAN miniport 驱动** (`qcusbwwan.sys`),在 INF 里声明接口类型为 `IF_TYPE_WWANPP` (243),Windows 的 **WWAN AutoConfig Service (WwanSvc)** 扫描到 WWAN 类型适配器后自动接管,显示在「设置 > 移动数据」面板。

驱动内部用 **QMI**(和我们 userland 完全相同的协议)控制拨号——只是它在内核态,我们在用户态。

## 驱动栈架构(4 个组件)

```
Windows「设置 > 网络和 Internet > 移动数据」面板
         ↕
Windows WWAN AutoConfig Service (WwanSvc)
    扫描 NDIS 适配器,发现 IfType=243 (WWANPP) → 接管
         ↕  NDIS miniport 上沿 (NDIS 6.2)
qcusbwwan.sys (583KB)  ← 核心:WWAN NDIS miniport 驱动
    ├── QMI 控制通道:拨号/断开/信号/运营商/SIM
    ├── QMAP 聚合:DL 32 包 × 32KB
    └── 数据通道:bulk EP 0x05 OUT / 0x88 IN (raw IP)
         ↕
qcusbfilter.sys (63KB)  ← USB lower filter 驱动
    └── QMI 接口多路复用 (MuxEnable, 最多 7 个 session)
         ↕
USB Composite Device (PID 2C7C:0125)
```

### 组件详解

| 组件 | 文件 | 大小 | 绑定接口 | 角色 |
|---|---|---|---|---|
| **WWAN miniport** | `qcusbwwan.sys` | 583KB | MI_04 | NDIS 6.2 miniport,IF_TYPE_WWANPP,QMI 拨号 + 数据收发 |
| **USB filter** | `qcusbfilter.sys` | 63KB | Composite | Lower filter,QMI MUX 多路复用 |
| **USB serial** | `qcusbser.sys` | 256KB | MI_00~03 | AT/DM/NMEA/Modem 串口(COM3~5) |
| **INF × 4** | qcwwan/qcfilter/qcser/qcmdm.inf | — | — | 硬件 ID 映射 + 驱动属性声明 |

### 设备管理器实测(Device Manager)

安装后 PID 0125 解出 6 个设备节点:

| 接口 | 设备管理器名称 | 驱动 | 类别 |
|---|---|---|---|
| Composite | Quectel USB Composite Device (0028) | usbccgp + qcusbfilter | USB |
| MI_00 | Quectel USB DM Port (COM3) | qcusbser | Ports |
| MI_01 | Quectel USB NMEA Port (COM4) | qcusbser | Ports |
| MI_02 | Quectel USB AT Port (COM5) | qcusbser | Ports |
| MI_03 | Quectel USB Modem | qcmdm | Modem |
| **MI_04** | **Quectel Wireless Ethernet Adapter** | **qcusbwwan** | **Net** |

注意 MI_04 的适配器名叫 "Wireless **Ethernet** Adapter",但实际 InterfaceType=243 (WWAN),不是 Ethernet。

## 关键 INF 声明(`qcwwan.inf`)

```ini
[Version]
Class       = Net                                    ; 网络适配器
ClassGUID   = {4d36e972-e325-11ce-bfc1-08002be10318}

; 硬件 ID:EC25/QDC507 QMI 模式,绑定 MI_04
%Quectel0125% = qcwwan.ndi, USB\VID_2C7C&PID_0125&MI_04

[qcwwan.ndi]
Characteristics = 0x4          ; NCF_PHYSICAL
BusType         = 15           ; PNPBus

; ★★★ 这三行是"移动数据"面板出现的关键 ★★★
*IfType = 243                  ; IF_TYPE_WWANPP (Wireless WAN, 3GPP)
*MediaType = 9                 ; NdisMediumWirelessWan
*PhysicalMediaType = 8         ; NdisPhysicalMediumWirelessWan
EnableDhcp = 0

[qcwwan.Reg]
HKR, Ndi\Interfaces, UpperRange, 0, "flpp4, flpp6"   ; IPv4/IPv6 上沿
HKR, Ndi\Interfaces, LowerRange, 0, "ppip"            ; 下沿

; QMI / QMAP 配置(注册表)
HKR,, QCMPDisableQMI, 0x00010001, 0x00000000          ; QMI 启用(注释掉的 = 不禁用)
HKR,, QCMPEnableQMAPV3, 0x00010001, 0x00000000        ; QMAP V3 禁用
HKR,, QCMPEnableQMAPV2, 0x00010001, 0x00000000        ; QMAP V2 禁用
HKR,, QCDriverDLMaxPackets, 0x00010001, 0x00000020    ; DL 聚合 32 包
HKR,, QCDriverDLAggregationSize, 0x00010001, 0x00008000 ; DL 聚合 32KB
```

### 为什么这三行声明能触发「移动数据」面板?

1. **`IF_TYPE_WWANPP` (243)** — IANA 定义的接口类型,表示 "3GPP Mobile Broadband"。Windows WwanSvc 启动时枚举所有 NDIS 适配器,IfType=243 → 标记为 WWAN 设备
2. **`NdisMediumWirelessWan` (9)** — NDIS 媒体类型,告诉 NDIS 栈这是无线 WAN(不是 Ethernet/Wi-Fi)
3. **`NdisPhysicalMediumWirelessWan` (8)** — 物理媒体类型,进一步确认

WwanSvc 发现 WWAN 适配器后:
- 通过 NDIS OID 查询设备能力(`OID_WWAN_*` 系列)
- 查询信号强度、运营商名称、SIM 状态、注册状态
- 创建 Mobile Broadband 连接配置文件
- 在「设置 > 移动数据」显示开关按钮、信号条、运营商名、APN 配置

## 实测验证

### `netsh mbn show interfaces`(驱动安装后)

```
名称:          手机网络
描述:          Quectel Wireless Ethernet Adapter
状态:          已连接
设备类型:      移动宽带设备被嵌入到系统中
设备 ID:       860000000000000          ← IMEI(脱敏)
型号:          QUECTEL Mobile Broadband Module
固件版本:      QDC507GLEFM21
提供商名称:    Carrier
信号:          70%
RSSI:          22 (-69 dBm)
```

### `Get-NetAdapter`

```
InterfaceType : 243              ← IF_TYPE_WWANPP
Status        : Up
LinkSpeed     : 150 Mbps
```

### 分配的 IP(由 WwanSvc 自动拨号)

```
IPv4: 10.0.0.1/30                ← 与 userland QMI 同一 IP 池(脱敏)
IPv6: 2001:db8::/64              ← 双栈,与 userland 同一前缀(脱敏)
```

## 与 userland 方案的对比

| 维度 | 官方驱动 (`qcusbwwan.sys`) | Userland (`qmidial` + TUN) |
|---|---|---|
| **USB 接口** | MI_04 (相同!) | MI_04 (相同!) |
| **控制协议** | QMI (相同!) | QMI (相同!) |
| **DTR 前置** | qcusbfilter 在 bind 时设 | 我们在 qmitransport 设 |
| **数据路径** | NDIS miniport → Windows TCP/IP 栈 | bulk EP → TUN relay → Windows TCP/IP |
| **「移动数据」面板** | ✅ WwanSvc 全管理 | ❌ TUN 是虚拟适配器,非 WWAN 类型 |
| **信号/运营商/APN 面板** | ✅ WwanSvc 通过 OID 查询 | ❌ 我们自己在日志输出 |
| **驱动类型** | 内核态 NDIS 6.2 miniport | 用户态(零内核驱动) |
| **macOS / Linux** | ❌ 仅 Windows | ✅ 三平台 |
| **Zadig/WinUSB 冲突** | 需要,会抢占 MI_04 | 需要,也会抢占 MI_04 |

## 核心差异:为什么 userland 不能显示在「移动数据」

「移动数据」面板的本质是 **WwanSvc 扫描 `IF_TYPE_WWANPP` 类型的 NDIS 适配器**。我们的 TUN 适配器是 `IF_TYPE_TUNNEL` (131),不是 WWAN,所以不会出现。

要让 userland 也出现在面板中,需要满足以下条件之一:

1. **内核态 NDIS WWAN miniport 驱动** — 写一个 `.sys`,INF 声明 `IF_TYPE_WWWANPP`,内部委托 QMI 给用户态进程。**但这就违背了纯用户态的初衷。**

2. **IP Helper API 虚拟适配器** — `CreateVirtualNetworkAdapter` 可以指定 IfType,但 Windows 不支持把用户态虚拟适配器声明为 WWAN 类型(WWAN 需要实际硬件设备)。

3. **不做面板,做 Win32 GUI** — 自己写一个 UI 显示信号/拨号/断开,功能等价但不嵌入 Windows Settings。**这是最务实的方案。**

结论:**userland 方案与「移动数据」面板互斥**。面板需要内核态 WWAN miniport;userland 选择 TUN 就必然是虚拟适配器。这是架构层面的 tradeoff,不是 bug。
