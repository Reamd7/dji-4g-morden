# 用户态 USB 4G Modem 可行性研究报告

> 研究目标：在 Windows 不安装 Quectel 厂商驱动的前提下，用纯用户态代码（Go/Rust）通过 USB 直接和 DJI 4G 模块（Quectel QDC507 / EG25-G）通信，实现 SIM 卡的上网和短信收发。
>
> 不包含 VoWiFi/VoLTE——那些需要 IMS 核心网配合，不在本研究范围。

---

## 一、为什么需要"不装驱动"的方案

当前方案（装 Quectel 驱动）的局限：

1. **平台依赖**：需要不同平台各自的厂商驱动（Windows 用 Quectel V2.2.4 / Linux 用 qmi_wwan 内核模块 / macOS 完全不支持）
2. **macOS 死局**：macOS 没有 qmi_wwan 驱动，不认 Vendor Specific (0xFF) 串口，ECM/NCM/MBIM 又被 DJI 抹掉的 IAD 描述符挡住。唯一能用的是 RNDIS 模式（一行 AT 切换），但切了就丢 AT 口
3. **内核依赖**：飞牛 NAS 的 trim 内核缺 qmi_wwan，需要自编译（虽然已解决，但每次内核升级都要重编）
4. **驱动黑盒**：Quectel 驱动是闭源的，无法定制或排障内部行为

"不装驱动"的方案把内核驱动做的事情（创建 COM 端口、创建 cdc-wdm 设备、创建 wwan0 网卡）全部搬到用户态用 Go/Rust 实现。

---

## 二、技术架构

### 2.1 整体设计

```
┌─ 用户态程序（Go 或 Rust，跨平台编译）────────────────────────┐
│                                                             │
│  ┌─ 协议层（平台无关的纯代码，现成可复用）─────────────────┐ │
│  │  QMI 协议栈（QMUX 帧 + CTL/DMS/NAS/WDS/WDA/UIM/WMS）   │ │
│  │  AT 命令 + APDU 编解码（AT+CSIM/CMGS/CMGL）            │ │
│  │  eSIM LPA 协议（Profile 下载/切换/删除）                │ │
│  │  SMS PDU 编解码（GSM7/UCS2/UDH 长短信）                 │ │
│  └─────────────────────────────────────────────────────────┘ │
│          ↕ qmiTransport / io.ReadWriteCloser 接口            │
│  ┌─ USB transport 适配层（需要自己写，~200 行）─────────────┐ │
│  │  libusb/gousb/nusb → 直接读写 USB bulk endpoint          │ │
│  │  替代内核的 cdc-wdm 驱动和 option 串口驱动               │ │
│  └─────────────────────────────────────────────────────────┘ │
│          ↕ USB bulk transfer                                 │
│  ┌─ 物理层（模块的 USB 接口）───────────────────────────────┐ │
│  │  Iface 2: AT 命令通道（EP OUT + EP IN）                  │ │
│  │  Iface 4: QMI 数据通道（EP OUT + EP IN + Interrupt）     │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                             │
│  ┌─ 虚拟网卡（现成跨平台库）────────────────────────────────┐ │
│  │  Linux   → /dev/net/tun（内核内置）                      │ │
│  │  Windows → Wintun.dll（WireGuard 团队开发，40KB，免签名） │ │
│  │  macOS   → utun（AF_SYSTEM socket，内核内置）             │ │
│  │                                                           │ │
│  │  QMI WDS 拨号拿到 IP → 注入 TUN → 系统有了虚拟网卡        │ │
│  └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘

前置条件（一次性）：
  Windows → Zadig 装 WinUSB（10 秒，不需要厂商驱动）
  macOS   → 无需（IOKit 内置）
  Linux   → 无需（可用 /dev/cdc-wdm 更简单，也可走 libusb）
```

### 2.2 核心思想

内核驱动做的三件事，在用户态分别用三个积木替代：

| 内核驱动做的事 | 用户态替代方案 | 现成库 |
|---|---|---|
| 创建 /dev/ttyUSB（串口设备） | libusb 直接读写 USB bulk endpoint | gousb (Go) / nusb (Rust) |
| 创建 /dev/cdc-wdm（QMI 控制设备） | 同上（QMI 走的是同一个 USB endpoint） | 同上 |
| 创建 wwan0（网络接口） | TUN 虚拟网卡 + QMI WDS 拨号 | wireguard/tun (Go) / tun crate (Rust) |

---

## 三、代码资产评估

### 3.1 Go 生态（vohive-collection）

这是本次研究最重要的发现。`source/vohive-collection/` 收集了 VoHive 全套依赖的源码快照，其中三个库提供了完整的协议栈实现：

#### quectel-qmi-go（~5000 行）

完整的 QMI 协议栈。实现了 10 个 QMI service：

| Service | 能力 |
|---|---|
| CTL | 客户端 ID 分配、proxy 模式 |
| DMS | 设备信息、IMEI、运行模式、PIN |
| NAS | 驻网状态、信号（RSRP/RSRQ）、搜网、制式偏好 |
| WDS | **拨号/断开**、APN profile 管理、流量统计、IP 分配 |
| WDA | Raw-IP / 数据格式配置 |
| UIM | 卡状态、PIN、**APDU 透明读卡**、逻辑通道 |
| WMS | **短信发送/读取/列举/删除**、路由、ACK |
| IMS | IMS 服务开关 |
| IMSA | IMS 注册状态 |
| VOICE | 拨号/接听/挂断、USSD |

transport 层架构关键——它定义了一个 `qmiTransport` 接口（只有 Read/Write/Close/SetReadDeadline 四个方法），上层协议代码全部依赖这个接口。当前只有 Linux cdc-wdm 的实现（`os.OpenFile("/dev/cdc-wdm0")`），但替换成 USB endpoint 实现只需 ~100 行。

还包含 `pkg/manager/` 高层管理器：自动拨号、自动重连、IPv4/IPv6 双栈、QMAP Mux 多 PDN、设备自动发现、短信收发事件。

还有 `pkg/netcfg/` 跨平台网络配置——已实现 Linux（netlink）、Windows（netsh/ipconfig）、macOS（ifconfig/route）三个平台的 Configurator。macOS 的 `darwin.go` 完整实现了 SetIPAddress/AddDefaultRoute/BringUp/SetMTU 等。

#### uicc-go（~2000 行）

SIM/USIM/ISIM 协议库。实现了多种 transport：

`at/` — 通过 AT+CSIM 发 APDU。`at.go` 的 Reader 只依赖 `io.ReadWriteCloser`。有 `serial_linux.go`（unix termios）、`serial_windows.go`（Windows CreateFile + DCB）、`serial_other.go`（其他平台返回 unsupported）。

`ccid/` — 通过 PC/SC CCID 读卡器发 APDU。

`mbim/` — 通过 MBIM UICC 底层访问发 APDU。

`qcom/` — 通过 QMI UIM 或 QRTR 发 APDU。

`usim/` — 高层 USIM/ISIM 操作：ICCID/IMSI 读取、AKA 认证、EAP-AKA、SMS-PP 下载。

关键：AT transport 的接口是 `io.ReadWriteCloser`——换成 USB endpoint 实现就能跨平台。

#### euicc-go（~3000 行）

eSIM 全生命周期管理。实现了完整的 LPA（Local Profile Assistant）协议：Profile 列表/下载/启用/停用/删除/重命名、SM-DP+ 通信、eUICC 元数据读取。

`driver/` 层支持多种 transport：AT（通过 uicc-go 的 at.Reader）、CCID（PC/SC）、MBIM、QMI、HTTP。

### 3.2 Go 生态代码复用评估

| 组件 | 现成度 | 平台无关 | 需要改动 |
|---|---|---|---|
| QMI 协议栈（quectel-qmi-go/pkg/qmi/） | ✅ 完整 | ✅ 纯 Go | 0 行 |
| QMI 连接管理器（quectel-qmi-go/pkg/manager/） | ✅ 完整 | ✅ 纯 Go | 0 行 |
| QMI transport（quectel-qmi-go/pkg/qmi/transport.go） | ⚠️ 只有 Linux | ❌ 写死 os.OpenFile | ~100 行 USB 实现 |
| AT + APDU（uicc-go/at/） | ✅ 完整 | ✅ 纯 Go | 0 行 |
| AT serial transport（uicc-go/at/serial_*.go） | ⚠️ Linux+Windows COM | ❌ 依赖 COM 端口 | ~100 行 USB 实现 |
| USIM/ISIM 高层（uicc-go/usim/） | ✅ 完整 | ✅ 纯 Go | 0 行 |
| eSIM LPA（euicc-go/lpa/） | ✅ 完整 | ✅ 纯 Go | 0 行 |
| 网络配置（quectel-qmi-go/pkg/netcfg/） | ✅ 三平台 | ✅ 已有 darwin/linux/windows | 0 行 |
| SMS PDU 编解码（sms-gateway pdu.go） | ✅ 完整 | ✅ 纯 Go | 0 行 |
| TUN 虚拟网卡 | ✅ wireguard/tun | ✅ 三平台 | ~50 行桥接 |
| QMI WDS → TUN 数据桥 | ❌ 无 | — | ~200 行 |
| **总计新增代码** | | | **~450 行** |

### 3.3 Rust 生态评估

#### nusb（纯 Rust USB 库）

nusb 是最大亮点——纯 Rust 实现的 USB 库，零 C 依赖。三平台后端：Windows 用 WinUSB、macOS 用 IOKit、Linux 用 usbfs。`cargo build` 直接出三平台二进制，不需要 CGO/libusb/mingw。

Go 的 gousb 需要 cgo 绑定 libusb-1.0，交叉编译麻烦（Windows 上需要 mingw 的 libusb）。nusb 在这一点上明显优于 gousb。

#### SimAdmin（31K 行 Rust）

这是 claude_plan.md 里提到的"28K lines covering IKEv2, EAP-AKA, IMS SIP"。但实际审查后发现**它不包含任何底层协议代码**。

SimAdmin 的架构：一切模组操作通过 ModemManager 的 D-Bus 接口（`org.freedesktop.ModemManager1`）。代码里全是 zbus D-Bus 调用——`MM_MODEM`、`MM_SIM`、`MM_MESSAGING`、`MM_VOICE`。

没有 QMI 协议栈、没有 AT 命令实现、没有 APDU 编解码、没有 USB 通信。eSIM 管理靠调用外部 `lpac` 命令行工具。短信收发走 ModemManager 的 Messaging D-Bus 接口。

**SimAdmin 的代码在 Windows/macOS 上完全无法使用**——ModemManager 是 Linux 专属的服务，不存在于 Windows/macOS。

#### Rust 生态代码复用评估

| 组件 | Rust 现成度 | 说明 |
|---|---|---|
| QMI 协议栈 | ❌ 无 | 需从零写或从 quectel-qmi-go 移植（~5000-7000 行） |
| AT + APDU | ❌ 无 | 需从零写或从 uicc-go 移植（~2000 行） |
| eSIM LPA | ❌ 无 | 可调用外部 lpac 工具（C 写的） |
| IMS / VoWiFi | ❌ SimAdmin 没有 | claude_plan 的说法不准确 |
| USB 通信 | ✅ nusb | 纯 Rust，零依赖，优于 Go 的 gousb |
| TUN 网卡 | ✅ tun / wintun-rs | 三平台支持 |
| 模组管理业务 | ⚠️ SimAdmin 有但绑 D-Bus | 无法跨平台 |
| **总计新增代码** | | **~7000-9000 行** |

### 3.4 Go vs Rust 结论

| 维度 | Go | Rust |
|---|---|---|
| 协议栈现成度 | ✅ 三个库（~10K 行）可直接复用 | ❌ 从零写（~7-9K 行） |
| USB 库 | gousb（cgo 绑 libusb） | nusb（纯 Rust，零依赖）✅ |
| TUN 库 | wireguard/tun ✅ | tun / wintun-rs ✅ |
| 交叉编译 | CGO 增加复杂度 | cargo build 直接三平台 ✅ |
| 开发效率 | 快（goroutine + 简单语法） | 慢（borrow checker + 编译速度） |
| 性能 | 足够（LTE Cat 4 用不满） | 略优（零拷贝 + 精确调度） |
| 新增代码量 | ~450 行 | ~7000-9000 行 |

**Go 路线明显更务实。** vohive-collection 的协议栈代码是核心资产，只有 Go 版本存在。Rust 唯一的优势（nusb 零依赖）不足以弥补缺少协议库的劣势。

如果将来 Rust 生态出现了完整的 QMI/AT 库（或有人把 quectel-qmi-go 移植到 Rust），可以重新评估。但在当前时间点，Go 是唯一可行的选择。

---

## 四、USB 接口分析

### 4.1 DJI 私有模式（mode 0, PID 2CA3:4006）

来自 `source/dji-cellular-as-modem/RESEARCH_NOTES.md` 的分析：

```
Iface 0: class FF/FF/FF → 2 bulk endpoints（QMI data）
Iface 1-3: class FF/00/00 → int + 2 bulk（ADB-like 通道）
Iface 4: class FF/FF/FF → int + 2 bulk（QMI control 候选）
```

关键发现：CDC Union 描述符损坏（bogus, master=0, slave=0）。标准 qmi_wwan 会绑到 ADB 接口（有 interrupt endpoint 的 Iface 1-3），不是真正的 QMI control。错位绑定后 cdc-wdm 无响应。

AT 命令通道实际在 Interface 3（不是标准 Quectel 的 Interface 2）。endpoint 是 EP 0x04 OUT / EP 0x86 IN。

用 pyusb 接管 Interface 0 和 4 发送 QMI/MBIM 协议数据包均超时——DJI 固件用私有协议。

结论：DJI 私有模式下标准协议无效，必须改 PID 或切模式。

### 4.2 标准 EC25 模式（PID 2C7C:0125）

刷成标准 Quectel EC25 PID 后，USB 接口布局变成标准的：

```
Iface 0 (MI_00): DM 诊断口 → ttyUSB0 / COM3
Iface 1 (MI_01): NMEA GPS → ttyUSB1 / COM4
Iface 2 (MI_02): AT 命令口 → ttyUSB2 / COM5
Iface 3 (MI_03): Modem 控制 → Modem 设备
Iface 4 (MI_04): QMI 数据通道 → cdc-wdm0 + wwan0 / NDIS 网卡
```

在这个模式下，标准 QMI 和 AT 协议正常工作。用户态方案应该基于这个模式。

### 4.3 USB endpoint 地址（用户态 USB 通信需要）

要用 libusb/nusb/gousb 直接通信，需要知道每个接口的 endpoint 地址。

Iface 2（AT 命令口）：通常有 2 个 bulk endpoint——一个 OUT（主机→模块），一个 IN（模块→主机）。具体地址可以用 `lsusb -v -d 2c7c:0125` 或 `usbconfig dump_device_pid=0x0125` 查看。dji-cellular-as-modem 的 at_send.py 在 DJI 模式下用的是 EP 0x04 OUT / EP 0x86 IN（Iface 3），但 EC25 模式下接口号和 endpoint 地址可能不同，需要用 `lsusb -v` 确认。

Iface 4（QMI 数据通道）：有 3 个 endpoint——1 个 interrupt IN（URC/通知）、1 个 bulk OUT（命令/数据）、1 个 bulk IN（响应/数据）。cdc-wdm 驱动用的就是这 3 个 endpoint。用户态方案需要 claim 这个接口并打开这 3 个 endpoint。

---

## 五、各平台前置条件

### Windows

需要用 [Zadig](https://zadig.akeo.ie/) 给目标接口安装 WinUSB 驱动（一次性操作，约 10 秒）。WinUSB 是 Microsoft 的通用 USB 驱动，不需要厂商签名。

之后 libusb（通过 gousb 的 cgo 绑定）或 Go 的纯 Go USB 库可以直接访问设备。

Wintun DLL 用于创建虚拟网卡。不需要安装，随程序分发。

### macOS

无需任何前置操作。libusb 底层用 IOKit（系统内置）。TUN 用 utun（AF_SYSTEM socket，内核内置）。

### Linux

无需任何前置操作。可以直接用 libusb（或走 /dev/cdc-wdm 更简单）。TUN 用 /dev/net/tun（内核内置）。

---

## 六、实现路线图

### 阶段 1：AT 通道 + 短信（最低难度，快速验证）

目标：通过 USB 直连发 AT 命令，实现短信收发。不需要数据通道。

1. 用 gousb 打开模块（VID 0x2C7C PID 0x0125），claim Iface 2，打开 bulk IN/OUT endpoint
2. 实现一个 `io.ReadWriteCloser` 包装 USB endpoint（~100 行）
3. 把这个 Reader 传给 uicc-go 的 `at.Reader`（零改动）
4. 用 uicc-go 的 APDU 功能或直接 AT+CMGS/CMGL 收发短信
5. 复用 sms-gateway 的 `pdu.go` 做 PDU 编解码

前置条件：Windows 上用 Zadig 给 Iface 2 装 WinUSB。

预计代码量：~200 行新代码 + ~2000 行现成协议代码复用。

### 阶段 2：QMI 通道 + 拨号（中等难度）

目标：通过 USB 直连 QMI，实现数据拨号。

1. 用 gousb claim Iface 4，打开 3 个 endpoint（interrupt + bulk IN + bulk OUT）
2. 实现一个 `qmiTransport`（Read/Write/Close/SetReadDeadline）包装 USB endpoint（~100 行）
3. 用 quectel-qmi-go 的 WDS service 发起拨号
4. 用 WDA service 配置 raw-IP 数据格式
5. 拿到运营商分配的 IP

前置条件：Windows 上用 Zadig 给 Iface 4 装 WinUSB。

预计代码量：~150 行新代码 + ~5000 行现成协议代码复用。

### 阶段 3：TUN 虚拟网卡 + 上网（最高难度）

目标：把 QMI 数据通道的 IP 包注入系统网络栈，实现真实上网。

1. 用 wireguard/tun 库创建虚拟网卡（~10 行）
2. 把 QMI WDS 拿到的 IP 配置到 TUN 上（用 netcfg 的 SetIPAddress）
3. 从 QMI bulk IN endpoint 读 IP 包 → 写入 TUN（~100 行）
4. 从 TUN 读 IP 包 → 写入 QMI bulk OUT endpoint（~100 行）
5. 处理 MTU、路由、DNS

预计代码量：~250 行新代码。

### 总计

三个阶段加起来约 600 行新代码，80% 的协议逻辑都有现成纯 Go 实现可直接复用。

---

## 七、替代方案对比

### 方案 A：用户态 USB（本研究推荐）

如上所述。优点：跨平台、不依赖厂商驱动、完全可控。缺点：需要写 ~600 行代码、USB transport 适配有复杂度。

### 方案 B：装 Quectel 驱动（当前已实现）

在 Windows 上装 Quectel LTE&5G Windows USB Driver V2.2.4。在 Linux 上用内核 qmi_wwan 驱动。优点：零代码、原生性能、功能完整。缺点：macOS 不支持、驱动是黑盒、飞牛 trim 内核需自编 qmi_wwan。

### 方案 C：切 RNDIS 模式（dji-cellular-as-modem 方案）

用 `AT+QCFG="usbnet",1` 切到 RNDIS。优点：一行命令、三平台免驱即插即用。缺点：丢失 AT 口（不能收发短信/管 eSIM）、NAT 架构（双重 NAT、无公网 IP）、不能做代理池。

### 方案 D：虚拟机（dji-4g-vohive-mac 方案）

在 macOS 上用 QEMU/UTM 跑 Linux 虚拟机 + USB 直通。优点：模块直通给 Linux，走标准 cdc-wdm。缺点：配置复杂、性能开销、虚拟机管理麻烦。

---

## 八、风险和不确定性

### 8.1 USB endpoint 地址的确认

需要用 `lsusb -v` 或 libusb 枚举确认 Iface 2 和 Iface 4 的具体 endpoint 地址。RESEARCH_NOTES 给出的是 DJI 私有模式下的地址（Iface 3），EC25 模式下可能不同。

### 8.2 QMI 数据格式

QMI 数据通道传输的可能是 raw-IP（直接是 IP 包）或 QMAP 封装。WDA service 有 SetDataFormat 命令，需要正确配置。如果模块默认是 QMAP 模式但 transport 没处理，收到的数据包格式会不对。

### 8.3 Windows 上的 USB 接口争用

如果 Iface 2 被 Quectel 驱动（qcser.sys）占用了，libusb 打不开它。需要先在设备管理器里卸载 Quectel 驱动或用 Zadig 替换成 WinUSB。这意味着方案 A 和方案 B 不能同时用（同一个接口不能同时被厂商驱动和 WinUSB 占用）。

### 8.4 macOS IOKit 的 USB 权限

macOS 上访问 USB 设备可能需要特定的 entitlement 或权限。libusb 通常能处理，但某些沙箱环境（如 App Store 应用）有限制。命令行程序通常没问题。

### 8.5 性能

用户态 USB bulk transfer 的延迟比内核驱动高（多一次用户态↔内核态拷贝）。但 LTE Cat 4 的带宽（150Mbps）远低于 USB 2.0 的带宽（480Mbps），CPU 开销也可忽略。实测应该和内核驱动无明显差异。

---

## 九、参考代码和资料

### 源码仓库

| 仓库 | 路径 | 价值 |
|---|---|---|
| vohive-collection/quectel-qmi-go | source/vohive-collection/quectel-qmi-go/ | QMI 协议栈（~5000 行） |
| vohive-collection/uicc-go | source/vohive-collection/uicc-go/ | AT + APDU（~2000 行） |
| vohive-collection/euicc-go | source/vohive-collection/euicc-go/ | eSIM LPA（~3000 行） |
| sms_gateway | source/sms_gateway/ | SMS PDU 编解码 + agent 架构参考 |
| dji-cellular-as-modem | source/dji-cellular-as-modem/ | USB 拓扑分析 + pyusb AT 工具 |
| SimAdmin | source/SimAdmin/ | Rust 模组管理（绑 D-Bus，不可跨平台复用） |

### 工具和库

| 库 | 语言 | 用途 |
|---|---|---|
| gousb | Go | USB 通信（cgo 绑 libusb） |
| nusb | Rust | USB 通信（纯 Rust，零依赖） |
| wireguard/tun | Go | 跨平台 TUN 虚拟网卡 |
| wintun-rs | Rust | Windows TUN |
| Zadig | 工具 | Windows 安装 WinUSB 通用驱动 |
| pyusb | Python | USB 通信（dji-cellular-as-modem 用） |

### 社区参考

- dji-cellular-as-modem 的 at_send.py — 验证了 pyusb 直连 USB 发 AT 命令可行
- WireGuard 的 tun 库 — 验证了跨平台用户态 TUN 可行
- quectel-qmi-go 的 darwin.go/windows.go — 作者已考虑了跨平台
- SimAdmin 的 ModemManager D-Bus 架构 — 反面教材：完全依赖 Linux 服务，不可移植

---

## 十、结论

在 Windows 不安装 Quectel 厂商驱动的前提下，用 Go 实现 SIM 卡的短信收发和上网是**完全可行的**。

核心技术路线：Zadig 装 WinUSB → gousb 直连 USB endpoint → 替换 quectel-qmi-go/uicc-go 的 transport 层 → 复用全部协议栈 → wireguard/tun 创建虚拟网卡 → QMI WDS 拨号上网。

预计新增代码量约 600 行，80% 的协议逻辑可直接复用 vohive-collection 里的现成 Go 代码。

Rust 路线在当前时间点不可行——缺少 QMI/AT/APDU 协议库，SimAdmin 的 Rust 代码绑死 ModemManager D-Bus 无法跨平台。如果将来有人把 quectel-qmi-go 移植到 Rust，可以重新评估。

macOS 的死局（不支持 QMI、ECM/NCM/MBIM 被 IAD 描述符缺陷挡住）在用户态方案下可以破解——libusb 通过 IOKit 访问 USB，utun 创建虚拟网卡，Go 代码跨平台编译。这是唯一能让 macOS 获得完整 4G 模组管理能力的路径。
