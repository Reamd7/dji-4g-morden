# DJI 4G 模块硬件分析与刷写研究

> 对 DJI "百望"（Baiwang）4G/5G 蜂窝模块的硬件架构、USB 协议、刷写方法、跨平台兼容性的深度研究报告。

---

## 一、硬件身份

### 1.1 产品信息

产品代号：DJI Baiwang（百望）

AT 身份查询（`ATI` 命令返回）：
```
Baiwang
QDC507
Revision: QDC507GLEFM21_01.001.02.004_BETA0611
```

底层芯片：Quectel QDC507，属于 EG25-G 家族。基带平台：Qualcomm SDX507（MDM9607 系列的衍生）。LTE Cat 4：下行 150 Mbps / 上行 50 Mbps。USB 2.0 High Speed（480 Mbps）。5 个 USB Interface。板载 SIM 卡槽 + eSIM 芯片。

### 1.2 USB 身份

原始（DJI 私有）：VID = 0x2CA3（DJI Technology Co., Ltd.），PID = 0x4006。所有接口 class = 0xFF（Vendor Specific）。

刷写后（标准 Quectel EC25）：VID = 0x2C7C（Quectel Wireless Solutions），PID = 0x0125（EC25 LTE modem）。

bcdDevice（USB 设备版本）：3.18（对应固件内部版本号）。

iManufacturer 字符串：`BAIWANG`。iProduct 字符串：`Baiwang`。注意：即使刷了 PID，这些描述符字符串不变（PID 和描述符字符串是独立的 NVRAM 字段）。

### 1.3 和社区认知的差异

社区（包括 DJI 论坛帖子和 dji-4g-vohive-mac README）普遍认为 DJI 4G 模块一代底层是 Quectel EG25-G（4G）。但实际 `ATI` 返回的是 `QDC507`（5G 平台 SDX507）。

这个差异可能有两种解释：一是不同批次的模块用了不同芯片（早期批次是 EG25-G，后期批次是 QDC507）；二是 QDC507 是 EG25-G 的衍生型号（同一芯片平台的不同 SKU）。

无论哪种情况，AT 命令集和刷写方法是 Quectel 通用的，不影响操作。

---

## 二、DJI 私有模式的 USB 拓扑

来自 `source/dji-cellular-as-modem/RESEARCH_NOTES.md` 的深度 USB 描述符分析。

### 2.1 接口布局（DJI 私有模式，mode 0）

Iface 0：class 0xFF/0xFF/0xFF，2 个 bulk endpoint。疑似 QMI data 通道。

Iface 1-3：class 0xFF/0x00/0x00，各 1 个 interrupt + 2 个 bulk endpoint。疑似 ADB-like 私有通信通道。

Iface 4：class 0xFF/0xFF/0xFF，1 个 interrupt + 2 个 bulk endpoint。疑似 QMI control 候选。

### 2.2 CDC Union 描述符缺陷

关键发现：DJI 固件的 CDC Union 描述符是损坏的——`bogus, master=0, slave=0`。

这导致标准 qmi_wwan 驱动即使强制绑定到这些接口，也会绑错位置。qmi_wwan 按 interrupt endpoint 存在性做绑定，它绑到 Iface 1-3（ADB-like 通道，有 interrupt endpoint），而不是真正的 QMI control（Iface 0 或 4）。错位绑定后 cdc-wdm 设备无响应。

用 pyusb 直接接管 Interface 0 和 4 发送标准 QMI/MBIM 协议数据包，均超时。说明 DJI 固件在 mode 0 下用的是私有协议，不是标准 QMI/MBIM。

### 2.3 AT 命令通道

在 DJI 私有模式下，AT 命令通道在 Interface 3（不是标准 Quectel 的 Interface 2）。

Interface 3 的 class 是 0xFF/0x00/0x00。endpoint 是 EP 0x04 OUT（主机→模块）和 EP 0x86 IN（模块→主机）。

`source/dji-cellular-as-modem/tools/at_send.py` 在 Mac 上用 pyusb fallback 时，就是直接接管 Interface 3 的这两个 endpoint 发 AT 命令。

---

## 三、两种 USB 配置命令

### 3.1 AT+QCFG="usbcfg"（改 VID/PID）

改 USB 厂商 ID 和产品 ID。写入 NVRAM，永久生效。

语法：`AT+QCFG="usbcfg",VID,PID,diag,nmea,at,modem,net,UA,MBIM`

参数说明：
- VID：USB 厂商 ID（0x2C7C = Quectel）
- PID：USB 产品 ID（0x0125 = EC25）
- diag/nmea/at/modem/net：各接口启用/禁用（1 或 0）
- UA：USB Audio（0 = 禁用）
- MBIM：MBIM 模式（0 = QMI，1 = MBIM）

我们用的刷写值：`AT+QCFG="usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,0`（末尾 0,0 = QMI 模式）。

原始值（DJI）：`0x2CA3,0x4006,1,1,1,1,1,0,0`。

### 3.2 AT+QCFG="usbnet"（改 USB composition）

改 USB 接口的协议类型。写入 NVRAM，永久生效。

| N | 模式 | 接口类型 | Windows | macOS | Linux |
|---|---|---|---|---|---|
| 0 | QMI/DJI 私有 | Vendor Specific 0xFF | 仅装驱动后 | ❌ | 仅 qmi_wwan 后 |
| 1 | RNDIS | Remote NDIS | ✅ 原生 | ✅ 原生 | ✅ rndis_host |
| 2 | CDC-ECM | 以太网控制模型 | ⚠️ | ❌ | ✅ cdc_ether |
| 3 | CDC-NCM | 网络控制模型 | ⚠️ | ❌ | ✅ cdc_ncm |
| 4 | MBIM | 移动宽带接口 | ✅ Win10+ | ❌ | ✅ cdc_mbim |

macOS 不支持 ECM/NCM/MBIM 的原因：AppleUSBCDCCompositeDevice 接管了父设备，但 DJI 抹掉了 IAD（Interface Association Descriptor）描述符，导致 Apple 的 NCM/MBIM 驱动无法完成 binding。IOUSBInterface 子节点不出现，也就没有 network interface 和 DHCP。

### 3.3 两个命令的关系

互不冲突。可以组合使用：
- 只改 usbcfg（我们的方案）：VID/PID 变 Quectel，接口布局保持 QMI，保留 AT 口
- 只改 usbnet（dji-cellular-as-modem 方案）：VID/PID 保持 DJI，接口变 RNDIS/ECM/NCM/MBIM
- 都改：先改 usbnet 选接口类型，再改 usbcfg 选厂商身份

---

## 四、刷成 EC25 后的接口布局

### 4.1 标准 Quectel EC25 接口

| 接口 | 功能 | Linux 驱动 | Windows 驱动 | COM |
|---|---|---|---|---|
| Iface 0 (MI_00) | DM 诊断口 | option | qcser.sys | COM3 |
| Iface 1 (MI_01) | NMEA GPS | option | qcser.sys | COM4 |
| Iface 2 (MI_02) | AT 命令口 | option | qcser.sys | COM5 |
| Iface 3 (MI_03) | Modem 控制 | — | qcmdm.sys | — |
| Iface 4 (MI_04) | QMI 数据通道 | qmi_wwan | qcusbwwan.sys | — |

Iface 4 的 QMI 数据通道在 Linux 上创建 `/dev/cdc-wdm0`（QMI 控制字符设备）和 `wwan0`（网络接口）。在 Windows 上创建 NDIS 网卡（"Quectel Wireless Ethernet Adapter"）。

### 4.2 iManufacturer/iProduct 字符串

刷 PID 后 USB 描述符里的 iManufacturer 仍然是 "BAIWANG"，iProduct 仍然是 "Baiwang"——因为 PID 和描述符字符串是独立的 NVRAM 字段。

这导致 `lsusb` 显示 `ID 2c7c:0125 Quectel Wireless Solutions Co., Ltd. EC25 LTE modem`（从 USB ID 数据库查 PID 得到的名字），但 `/dev/serial/by-id/` 路径还是 `usb-BAIWANG_Baiwang-if02-port0`（从描述符字符串生成的）。

---

## 五、PID 选择依据

### 5.1 Windows 驱动支持的 PID

从 Quectel LTE&5G Windows USB Driver V2.2.4 的 .inf 文件提取的完整列表：

VID 2C7C（Quectel）：0121, 0125, 0161, 0191, 0195, 0201, 0296, 0306, 0415, 0435, 0452, 0455, 0456, 0512, 0620, 0700, 0800。

VID 05C6（Qualcomm emergency）：9008（EDL 应急下载模式）。

VID 3763（HMD/Nokia）：3C93。

### 5.2 为什么选 0x0125

`0x0125` 是唯一一个在 Quectel 驱动里 4 个 .inf（qcser/qcmdm/qcwwan/qcfilter）全部覆盖的 PID，同时在 Linux 内核的 option 和 qmi_wwan 驱动设备表里也有原生支持。

跨平台最优：Windows 4 个驱动全覆盖 + Linux 两个驱动原生支持 + 保留全部接口功能（AT + QMI + Modem）。

---

## 六、刷写流程

### 6.1 Linux 上刷写

完整步骤见 `DJI_Baiwang_flash_EC25_tutorial.md`。精简流程：

1. `modprobe option` + `echo "2ca3 4006" > /sys/bus/usb-serial/drivers/option1/new_id` 让 Linux 临时认出 DJI 模块
2. 找 AT 口（通常是 ttyUSB2）
3. 备份原始配置（`AT+QCFG="usbcfg"` 读取并记录）
4. 刷写：`AT+QCFG="usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,0`
5. 重启：`AT+CFUN=1,1`
6. 等 30 秒验证

重要提醒：用 `option` 驱动 + `option1/new_id`，不要用 `usbserial`/`generic`（社区有些教程写错了）。

### 6.2 Windows 上刷写

需要先装 Quectel 驱动或用 QCOM 工具。然后用 AT 终端发相同的刷写命令。

也可以先在 Linux 上刷好（一次永久生效），再插到 Windows 上使用。

### 6.3 恢复

刷成 EC25 后 AT 口还在（ttyUSB2），可以再发 `AT+QCFG` 改回去。用备份的原始值：

`AT+QCFG="usbcfg",0x2CA3,0x4006,1,1,1,1,1,0,0` + `AT+CFUN=1,1`

### 6.4 多模块刷写

两块模块同时插入时，给 option 注入一次 new_id 即可。第一块出 ttyUSB0~3，第二块出 ttyUSB4~7。AT 口分别在 ttyUSB2 和 ttyUSB6。逐块执行刷写流程。

---

## 七、模块固件卡死问题

### 7.1 现象

USB 枚举正常（ttyUSB + cdc-wdm 都在），但所有 ttyUSB 不响应 AT 命令（只回显不返回 OK/结果）。QMI 通道（qmicli cdc-wdm0）可能仍正常。

### 7.2 原因

模块 ARM 固件卡死（通常是异常掉电后未正常恢复），但 USB 枚举层（基带处理器独立处理）还在工作。

### 7.3 软件恢复尝试（全部失败）

- USB authorized 0→1：只重枚举 USB 总线，模块不断电
- USBDEVFS_RESET ioctl：同上
- uhubctl：Intel xHCI root hub "No power switching"，硬件不支持 per-port 电源控制

### 7.4 唯一可靠恢复方法

物理拔插 USB（给模块主芯片彻底断电）。拔 → 等 10 秒（电容放电）→ 插回。

软件层无法给 Intel SoC 上的 USB 口断电（VBUS 5V 常供电）。

---

## 八、跨平台兼容性矩阵

| 平台 | DJI 私有 (mode 0) | EC25 QMI (改 PID) | RNDIS (usbnet=1) |
|---|---|---|---|
| Linux (标准内核) | ❌ 需 new_id | ✅ option + qmi_wwan 免驱 | ✅ rndis_host 免驱 |
| Linux (飞牛 trim) | ❌ 需 new_id | ✅ option 免驱，qmi_wwan 需自编 | ✅ rndis_host 免驱 |
| Windows 10/11 | ❌ 需装 Quectel 驱动 | ❌ 需装 Quectel 驱动 (0xFF) | ✅ 原生 RNDIS 免驱 |
| macOS | ❌ | ❌ | ✅ 原生 RNDIS 免驱 |

注意：Windows 即使刷成了标准 Quectel PID `2C7C:0125`，也不能免驱——因为 EC25 的接口用的是 Vendor-Specific Class (0xFF)，Windows 内置驱动不认 0xFF，必须装 Quectel 驱动包。只有 RNDIS 模式才能真正免驱（因为 RNDIS 是标准 USB class）。
